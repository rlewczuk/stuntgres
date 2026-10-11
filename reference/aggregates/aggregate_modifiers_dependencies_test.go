package reference_test

import (
	"bytes"
	"database/sql"
	"stuntgres"
	"stuntgres/reference/fixture"
	"testing"
)

func TestReferenceAggregateModifiersDependencies(t *testing.T) {
	for _, tc := range []struct {
		name, create, expression, rename, drop, object, dependency string
		sequence                                                   bool
	}{
		{"relation", "CREATE TABLE am_dep_aux(v INT)", "count(*) FILTER(WHERE EXISTS(SELECT 1 FROM am_dep_aux WHERE v=7))", "ALTER TABLE am_dep_aux RENAME TO am_dep_retained", "DROP TABLE am_dep_retained", "table am_dep_retained", "table am_dep_retained", false},
		{"sequence_filter", "CREATE SEQUENCE am_dep_seq", "count(*) FILTER(WHERE nextval('am_dep_seq')>0)", "ALTER SEQUENCE am_dep_seq RENAME TO am_dep_retained", "DROP SEQUENCE am_dep_retained", "sequence am_dep_retained", "sequence am_dep_retained", true},
		{"sequence_order", "CREATE SEQUENCE am_dep_seq", "count(v ORDER BY nextval('am_dep_seq'))", "ALTER SEQUENCE am_dep_seq RENAME TO am_dep_retained", "DROP SEQUENCE am_dep_retained", "sequence am_dep_retained", "sequence am_dep_retained", true},
		{"type", "CREATE DOMAIN am_dep_type AS integer", "count(*) FILTER(WHERE v>0::am_dep_type)", "", "DROP DOMAIN am_dep_type", "type am_dep_type", "type am_dep_type", false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			aggregateModifiersScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn, run func(fixture.Step)) {
				run(fixture.Exec("CREATE TABLE am_dep_source(v INT)", 0))
				run(fixture.Exec("INSERT INTO am_dep_source VALUES(1),(2)", 2))
				run(fixture.Exec(tc.create, 0))
				if tc.name == "relation" {
					run(fixture.Exec("INSERT INTO am_dep_aux VALUES(7)", 1))
				}
				run(fixture.Exec("CREATE VIEW am_dep_view AS SELECT "+tc.expression+" AS n FROM am_dep_source", 0))
				if tc.rename != "" {
					run(fixture.Exec(tc.rename, 0))
				}
				var definition string
				if err := c.QueryRowContext(p.Ctx, "SELECT pg_get_viewdef('am_dep_view'::regclass,true)").Scan(&definition); err != nil {
					t.Fatal(err)
				}
				run(fixture.Exec("CREATE VIEW am_dep_replay AS "+definition, 0))
				if !b.PG {
					var dump bytes.Buffer
					if err := stuntgres.Dump(p.Ctx, p.Native, &dump); err != nil {
						t.Fatal(err)
					}
					if err := stuntgres.Reset(p.Ctx, p.Native); err != nil {
						t.Fatal(err)
					}
					if err := stuntgres.Restore(p.Ctx, p.Native, bytes.NewReader(dump.Bytes())); err != nil {
						t.Fatal(err)
					}
				}
				for _, view := range []string{"am_dep_view", "am_dep_replay"} {
					aggregateModifiersBoth(run, fixture.Select("SELECT n FROM "+view, []string{"n"}, []string{"INT8"}, [][]any{{int64(2)}}))
				}
				if tc.sequence {
					// Eight public executions over two admitted rows consume sixteen
					// values; the renamed captured identity must be the same sequence.
					run(fixture.Select("SELECT nextval('am_dep_retained')", []string{"nextval"}, []string{"INT8"}, [][]any{{int64(17)}}))
				}
				failure := fixture.ImplicitError(tc.drop+" RESTRICT", fixture.Diagnostic{
					Code: "2BP01", Message: "cannot drop " + tc.object + " because other objects depend on it",
					Detail: "view am_dep_view depends on " + tc.dependency + "\nview am_dep_replay depends on " + tc.dependency,
					Hint:   "Use DROP ... CASCADE to drop the dependent objects too.",
				})
				failure.ErrorStage = "execute"
				for _, prepared := range []bool{false, true} {
					s := failure
					s.Prepared = prepared
					run(s)
				}
				if tc.name == "relation" {
					run(fixture.Exec("DELETE FROM am_dep_retained", 1))
					aggregateModifiersBoth(run, fixture.Select("SELECT n FROM am_dep_view", []string{"n"}, []string{"INT8"}, [][]any{{int64(0)}}))
				}
				run(fixture.Exec(tc.drop+" CASCADE", 0))
				for _, view := range []string{"am_dep_view", "am_dep_replay"} {
					aggregateModifiersBoth(run, aggregateModifiersError("SELECT n FROM "+view, "42P01", `relation "`+view+`" does not exist`, view))
				}
				aggregateModifiersBoth(run, fixture.Select("SELECT v FROM am_dep_source ORDER BY v", []string{"v"}, []string{"INT4"}, [][]any{{int64(1)}, {int64(2)}}))
			})
		})
	}
}
