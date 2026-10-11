package reference_test

import (
	"bytes"
	"strings"
	"stuntgres"
	"stuntgres/reference/fixture"
	"testing"
)

func TestReferencePGDependReviewCapturedReplay(t *testing.T) {
	for _, cycle := range []bool{false, true} {
		name := "ExternalOwners"
		schema := "pgdep_review_captured"
		if cycle {
			name = "DefaultCycle"
			schema = "pgdep_review_cycle"
		}
		t.Run(name, func(t *testing.T) {
			fixture.SchemaReferenceCleanup(t, schema)
			p := fixture.NewScenario(t)
			var nativeDump string
			setup := func(b fixture.Backend) {
				fixture.PgDependExec(t, p, b, "CREATE SCHEMA "+schema, "SET search_path TO "+schema+",public")
				if cycle {
					fixture.PgDependExec(t, p, b, "CREATE TABLE cycle_table(a INT,c BOOL,s BOOL)", "CREATE VIEW cycle_view AS SELECT a FROM cycle_table", "ALTER TABLE cycle_table ALTER c SET DEFAULT ('cycle_view'::regclass IS NOT NULL)", "ALTER TABLE cycle_table ALTER s SET DEFAULT ('cycle_table'::regclass IS NOT NULL)")
					fixture.RunStep(t, p.Ctx, b.DB, b.Recorder, fixture.Exec("INSERT INTO cycle_table(a) VALUES(7)", 1))
				} else {
					fixture.PgDependExec(t, p, b, "CREATE TABLE captured_provider(a INT)",
						"CREATE TABLE captured_owner(a INT,d BOOL DEFAULT ('captured_provider'::regclass IS NOT NULL),g BOOL GENERATED ALWAYS AS('captured_provider'::regclass IS NOT NULL) STORED,CONSTRAINT captured_check CHECK(a>0 AND 'captured_provider'::regclass IS NOT NULL))",
						"CREATE INDEX captured_index ON captured_owner(('captured_provider'::regclass IS NOT NULL))",
						"CREATE VIEW captured_view AS SELECT 'captured_provider'::regclass IS NOT NULL AS c")
					fixture.RunStep(t, p.Ctx, b.DB, b.Recorder, fixture.Exec("INSERT INTO captured_owner(a) VALUES(1)", 1))
				}
			}
			check := func(b fixture.Backend) {
				fixture.PgDependExec(t, p, b, "SET search_path TO "+schema+",public")
				if cycle {
					table := fixture.PgDependRelation(t, b, "cycle_table")
					view := fixture.PgDependRelation(t, b, "cycle_view")
					ns := fixture.PgDependNamespace(t, b, table)
					d := fixture.PgDependDefinition(t, b, table, 2)
					rule := fixture.PgDependRule(t, b, view)
					fixture.PgDependBag(t, p, b, 1259, table, fixture.PgDependEdge(1259, table, 0, 2615, ns, 0, "n"))
					fixture.PgDependBag(t, p, b, 1259, view, fixture.PgDependEdge(1259, view, 0, 2615, ns, 0, "n"))
					fixture.PgDependBag(t, p, b, 2604, d, fixture.PgDependEdge(2604, d, 0, 1259, table, 2, "a"), fixture.PgDependEdge(2604, d, 0, 1259, view, 0, "n"))
					fixture.PgDependBag(t, p, b, 2618, rule, fixture.PgDependEdge(2618, rule, 0, 1259, view, 0, "i"), fixture.PgDependEdge(2618, rule, 0, 1259, table, 1, "n"))
					self := fixture.PgDependDefinition(t, b, table, 3)
					fixture.PgDependBag(t, p, b, 2604, self, fixture.PgDependEdge(2604, self, 0, 1259, table, 3, "a"), fixture.PgDependEdge(2604, self, 0, 1259, table, 0, "n"))
					fixture.RunStep(t, p.Ctx, b.DB, b.Recorder, fixture.Select("SELECT a,c,s FROM cycle_table ORDER BY a", []string{"a", "c", "s"}, []string{"INT4", "BOOL", "BOOL"}, [][]any{{int64(7), true, true}}))
				} else {
					provider := fixture.PgDependRelation(t, b, "captured_provider")
					table := fixture.PgDependRelation(t, b, "captured_owner")
					view := fixture.PgDependRelation(t, b, "captured_view")
					index := fixture.PgDependRelation(t, b, "captured_index")
					ns := fixture.PgDependNamespace(t, b, table)
					d := fixture.PgDependDefinition(t, b, table, 2)
					g := fixture.PgDependDefinition(t, b, table, 3)
					ck := fixture.PgDependConstraint(t, b, table, "captured_check")
					rule := fixture.PgDependRule(t, b, view)
					for _, id := range []int64{provider, table, view} {
						fixture.PgDependBag(t, p, b, 1259, id, fixture.PgDependEdge(1259, id, 0, 2615, ns, 0, "n"))
					}
					fixture.PgDependBag(t, p, b, 2604, d, fixture.PgDependEdge(2604, d, 0, 1259, table, 2, "a"), fixture.PgDependEdge(2604, d, 0, 1259, provider, 0, "n"))
					fixture.PgDependBag(t, p, b, 2604, g, fixture.PgDependEdge(2604, g, 0, 1259, table, 3, "i"), fixture.PgDependEdge(2604, g, 0, 1259, provider, 0, "n"))
					fixture.PgDependBag(t, p, b, 2606, ck, fixture.PgDependEdge(2606, ck, 0, 1259, table, 1, "a"), fixture.PgDependEdge(2606, ck, 0, 1259, table, 1, "n"), fixture.PgDependEdge(2606, ck, 0, 1259, provider, 0, "n"))
					fixture.PgDependBag(t, p, b, 1259, index, fixture.PgDependEdge(1259, index, 0, 1259, table, 0, "a"), fixture.PgDependEdge(1259, index, 0, 1259, provider, 0, "n"))
					fixture.PgDependBag(t, p, b, 2618, rule, fixture.PgDependEdge(2618, rule, 0, 1259, view, 0, "i"), fixture.PgDependEdge(2618, rule, 0, 1259, provider, 0, "n"))
					fixture.RunStep(t, p.Ctx, b.DB, b.Recorder, fixture.Select("SELECT a,d,g FROM captured_owner ORDER BY a", []string{"a", "d", "g"}, []string{"INT4", "BOOL", "BOOL"}, [][]any{{int64(1), true, true}}))
					fixture.RunStep(t, p.Ctx, b.DB, b.Recorder, fixture.Select("SELECT c FROM captured_view", []string{"c"}, []string{"BOOL"}, [][]any{{true}}))
				}
			}
			consume := func(b fixture.Backend) {
				if cycle {
					fixture.RunStep(t, p.Ctx, b.DB, b.Recorder, fixture.Exec("INSERT INTO cycle_table(a) VALUES(8)", 1))
					fixture.RunStep(t, p.Ctx, b.DB, b.Recorder, fixture.Select("SELECT a,c,s FROM cycle_table ORDER BY a", []string{"a", "c", "s"}, []string{"INT4", "BOOL", "BOOL"}, [][]any{{int64(7), true, true}, {int64(8), true, true}}))
				} else {
					fixture.RunStep(t, p.Ctx, b.DB, b.Recorder, fixture.Exec("INSERT INTO captured_owner(a) VALUES(2)", 1))
					fixture.RunStep(t, p.Ctx, b.DB, b.Recorder, fixture.Select("SELECT a,d,g FROM captured_owner ORDER BY a", []string{"a", "d", "g"}, []string{"INT4", "BOOL", "BOOL"}, [][]any{{int64(1), true, true}, {int64(2), true, true}}))
				}
			}
			pgDependReviewBackends(t, p, func(t *testing.T, b fixture.Backend) {
				setup(b)
				check(b)
				if !b.PG {
					var dump bytes.Buffer
					if err := stuntgres.Dump(p.Ctx, p.Native, &dump); err != nil {
						t.Fatal(err)
					}
					nativeDump = dump.String()
					if err := stuntgres.Reset(p.Ctx, p.Native); err != nil {
						t.Fatal(err)
					}
					if err := stuntgres.Restore(p.Ctx, p.Native, strings.NewReader(nativeDump)); err != nil {
						t.Fatal("actual native Dump cannot restore", err)
					}
					check(b)
				}
				consume(b)
			})
			if nativeDump != "" && p.Postgres != nil {
				b := p.Backends()[1]
				fixture.PgDependExec(t, p, b, "DROP SCHEMA "+schema+" CASCADE")
				if _, err := b.DB.ExecContext(p.Ctx, nativeDump); err != nil {
					t.Fatal("pinned PG rejected actual native Dump", err)
				}
				check(b)
				consume(b)
			}
		})
	}
}
