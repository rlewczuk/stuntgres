package reference_test

import (
	"bytes"
	"database/sql"
	"stuntgres"
	"testing"
)

func TestReferenceAggregateModifiersDependencies(t *testing.T) {
	for _, tc := range []struct {
		name, create, expression, rename, drop, object, dependency string
		sequence bool
	}{
		{"relation", "CREATE TABLE am_dep_aux(v INT)", "count(*) FILTER(WHERE EXISTS(SELECT 1 FROM am_dep_aux WHERE v=7))", "ALTER TABLE am_dep_aux RENAME TO am_dep_retained", "DROP TABLE am_dep_retained", "table am_dep_retained", "table am_dep_retained", false},
		{"sequence_filter", "CREATE SEQUENCE am_dep_seq", "count(*) FILTER(WHERE nextval('am_dep_seq')>0)", "ALTER SEQUENCE am_dep_seq RENAME TO am_dep_retained", "DROP SEQUENCE am_dep_retained", "sequence am_dep_retained", "sequence am_dep_retained", true},
		{"sequence_order", "CREATE SEQUENCE am_dep_seq", "count(v ORDER BY nextval('am_dep_seq'))", "ALTER SEQUENCE am_dep_seq RENAME TO am_dep_retained", "DROP SEQUENCE am_dep_retained", "sequence am_dep_retained", "sequence am_dep_retained", true},
		{"type", "CREATE DOMAIN am_dep_type AS integer", "count(*) FILTER(WHERE v>0::am_dep_type)", "", "DROP DOMAIN am_dep_type", "type am_dep_type", "type am_dep_type", false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			aggregateModifiersScenario(t, func(t *testing.T, p *referencePair, b referenceBackend, c *sql.Conn, run func(referenceStep)) {
				run(referenceExec("CREATE TABLE am_dep_source(v INT)", 0))
				run(referenceExec("INSERT INTO am_dep_source VALUES(1),(2)", 2))
				run(referenceExec(tc.create, 0))
				if tc.name == "relation" {
					run(referenceExec("INSERT INTO am_dep_aux VALUES(7)", 1))
				}
				run(referenceExec("CREATE VIEW am_dep_view AS SELECT "+tc.expression+" AS n FROM am_dep_source", 0))
				if tc.rename != "" {
					run(referenceExec(tc.rename, 0))
				}
				var definition string
				if err := c.QueryRowContext(p.ctx, "SELECT pg_get_viewdef('am_dep_view'::regclass,true)").Scan(&definition); err != nil {
					t.Fatal(err)
				}
				run(referenceExec("CREATE VIEW am_dep_replay AS "+definition, 0))
				if !b.pg {
					var dump bytes.Buffer
					if err := stuntgres.Dump(p.ctx, p.native, &dump); err != nil {
						t.Fatal(err)
					}
					if err := stuntgres.Reset(p.ctx, p.native); err != nil {
						t.Fatal(err)
					}
					if err := stuntgres.Restore(p.ctx, p.native, bytes.NewReader(dump.Bytes())); err != nil {
						t.Fatal(err)
					}
				}
				for _, view := range []string{"am_dep_view", "am_dep_replay"} {
					aggregateModifiersBoth(run, referenceSelect("SELECT n FROM "+view, []string{"n"}, []string{"INT8"}, [][]any{{int64(2)}}))
				}
				if tc.sequence {
					// Eight public executions over two admitted rows consume sixteen
					// values; the renamed captured identity must be the same sequence.
					run(referenceSelect("SELECT nextval('am_dep_retained')", []string{"nextval"}, []string{"INT8"}, [][]any{{int64(17)}}))
				}
				failure := referenceImplicitError(tc.drop+" RESTRICT", referenceDiagnostic{
					Code: "2BP01", Message: "cannot drop "+tc.object+" because other objects depend on it",
					Detail: "view am_dep_view depends on "+tc.dependency+"\nview am_dep_replay depends on "+tc.dependency,
					Hint: "Use DROP ... CASCADE to drop the dependent objects too.",
				})
				failure.errorStage = "execute"
				for _, prepared := range []bool{false, true} {
					s := failure
					s.prepared = prepared
					run(s)
				}
				if tc.name == "relation" {
					run(referenceExec("DELETE FROM am_dep_retained", 1))
					aggregateModifiersBoth(run, referenceSelect("SELECT n FROM am_dep_view", []string{"n"}, []string{"INT8"}, [][]any{{int64(0)}}))
				}
				run(referenceExec(tc.drop+" CASCADE", 0))
				for _, view := range []string{"am_dep_view", "am_dep_replay"} {
					aggregateModifiersBoth(run, aggregateModifiersError("SELECT n FROM "+view, "42P01", `relation "`+view+`" does not exist`, view))
				}
				aggregateModifiersBoth(run, referenceSelect("SELECT v FROM am_dep_source ORDER BY v", []string{"v"}, []string{"INT4"}, [][]any{{int64(1)}, {int64(2)}}))
			})
		})
	}
}
