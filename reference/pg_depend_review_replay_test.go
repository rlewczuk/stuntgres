package reference_test

import (
	"bytes"
	"strings"
	"stuntgres"
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
			schemaReferenceCleanup(t, schema)
			p := newReferenceScenario(t)
			var nativeDump string
			setup := func(b referenceBackend) {
				pgDependExec(t, p, b, "CREATE SCHEMA "+schema, "SET search_path TO "+schema+",public")
				if cycle {
					pgDependExec(t, p, b, "CREATE TABLE cycle_table(a INT,c BOOL,s BOOL)", "CREATE VIEW cycle_view AS SELECT a FROM cycle_table", "ALTER TABLE cycle_table ALTER c SET DEFAULT ('cycle_view'::regclass IS NOT NULL)", "ALTER TABLE cycle_table ALTER s SET DEFAULT ('cycle_table'::regclass IS NOT NULL)")
					referenceRunStep(t, p.ctx, b.db, b.recorder, referenceExec("INSERT INTO cycle_table(a) VALUES(7)", 1))
				} else {
					pgDependExec(t, p, b, "CREATE TABLE captured_provider(a INT)",
						"CREATE TABLE captured_owner(a INT,d BOOL DEFAULT ('captured_provider'::regclass IS NOT NULL),g BOOL GENERATED ALWAYS AS('captured_provider'::regclass IS NOT NULL) STORED,CONSTRAINT captured_check CHECK(a>0 AND 'captured_provider'::regclass IS NOT NULL))",
						"CREATE INDEX captured_index ON captured_owner(('captured_provider'::regclass IS NOT NULL))",
						"CREATE VIEW captured_view AS SELECT 'captured_provider'::regclass IS NOT NULL AS c")
					referenceRunStep(t, p.ctx, b.db, b.recorder, referenceExec("INSERT INTO captured_owner(a) VALUES(1)", 1))
				}
			}
			check := func(b referenceBackend) {
				pgDependExec(t, p, b, "SET search_path TO "+schema+",public")
				if cycle {
					table := pgDependRelation(t, b, "cycle_table")
					view := pgDependRelation(t, b, "cycle_view")
					ns := pgDependNamespace(t, b, table)
					d := pgDependDefinition(t, b, table, 2)
					rule := pgDependRule(t, b, view)
					pgDependBag(t, p, b, 1259, table, pgDependEdge(1259, table, 0, 2615, ns, 0, "n"))
					pgDependBag(t, p, b, 1259, view, pgDependEdge(1259, view, 0, 2615, ns, 0, "n"))
					pgDependBag(t, p, b, 2604, d, pgDependEdge(2604, d, 0, 1259, table, 2, "a"), pgDependEdge(2604, d, 0, 1259, view, 0, "n"))
					pgDependBag(t, p, b, 2618, rule, pgDependEdge(2618, rule, 0, 1259, view, 0, "i"), pgDependEdge(2618, rule, 0, 1259, table, 1, "n"))
					self := pgDependDefinition(t, b, table, 3)
					pgDependBag(t, p, b, 2604, self, pgDependEdge(2604, self, 0, 1259, table, 3, "a"), pgDependEdge(2604, self, 0, 1259, table, 0, "n"))
					referenceRunStep(t, p.ctx, b.db, b.recorder, referenceSelect("SELECT a,c,s FROM cycle_table ORDER BY a", []string{"a", "c", "s"}, []string{"INT4", "BOOL", "BOOL"}, [][]any{{int64(7), true, true}}))
				} else {
					provider := pgDependRelation(t, b, "captured_provider")
					table := pgDependRelation(t, b, "captured_owner")
					view := pgDependRelation(t, b, "captured_view")
					index := pgDependRelation(t, b, "captured_index")
					ns := pgDependNamespace(t, b, table)
					d := pgDependDefinition(t, b, table, 2)
					g := pgDependDefinition(t, b, table, 3)
					ck := pgDependConstraint(t, b, table, "captured_check")
					rule := pgDependRule(t, b, view)
					for _, id := range []int64{provider, table, view} {
						pgDependBag(t, p, b, 1259, id, pgDependEdge(1259, id, 0, 2615, ns, 0, "n"))
					}
					pgDependBag(t, p, b, 2604, d, pgDependEdge(2604, d, 0, 1259, table, 2, "a"), pgDependEdge(2604, d, 0, 1259, provider, 0, "n"))
					pgDependBag(t, p, b, 2604, g, pgDependEdge(2604, g, 0, 1259, table, 3, "i"), pgDependEdge(2604, g, 0, 1259, provider, 0, "n"))
					pgDependBag(t, p, b, 2606, ck, pgDependEdge(2606, ck, 0, 1259, table, 1, "a"), pgDependEdge(2606, ck, 0, 1259, table, 1, "n"), pgDependEdge(2606, ck, 0, 1259, provider, 0, "n"))
					pgDependBag(t, p, b, 1259, index, pgDependEdge(1259, index, 0, 1259, table, 0, "a"), pgDependEdge(1259, index, 0, 1259, provider, 0, "n"))
					pgDependBag(t, p, b, 2618, rule, pgDependEdge(2618, rule, 0, 1259, view, 0, "i"), pgDependEdge(2618, rule, 0, 1259, provider, 0, "n"))
					referenceRunStep(t, p.ctx, b.db, b.recorder, referenceSelect("SELECT a,d,g FROM captured_owner ORDER BY a", []string{"a", "d", "g"}, []string{"INT4", "BOOL", "BOOL"}, [][]any{{int64(1), true, true}}))
					referenceRunStep(t, p.ctx, b.db, b.recorder, referenceSelect("SELECT c FROM captured_view", []string{"c"}, []string{"BOOL"}, [][]any{{true}}))
				}
			}
			consume := func(b referenceBackend) {
				if cycle {
					referenceRunStep(t, p.ctx, b.db, b.recorder, referenceExec("INSERT INTO cycle_table(a) VALUES(8)", 1))
					referenceRunStep(t, p.ctx, b.db, b.recorder, referenceSelect("SELECT a,c,s FROM cycle_table ORDER BY a", []string{"a", "c", "s"}, []string{"INT4", "BOOL", "BOOL"}, [][]any{{int64(7), true, true}, {int64(8), true, true}}))
				} else {
					referenceRunStep(t, p.ctx, b.db, b.recorder, referenceExec("INSERT INTO captured_owner(a) VALUES(2)", 1))
					referenceRunStep(t, p.ctx, b.db, b.recorder, referenceSelect("SELECT a,d,g FROM captured_owner ORDER BY a", []string{"a", "d", "g"}, []string{"INT4", "BOOL", "BOOL"}, [][]any{{int64(1), true, true}, {int64(2), true, true}}))
				}
			}
			pgDependReviewBackends(t, p, func(t *testing.T, b referenceBackend) {
				setup(b)
				check(b)
				if !b.pg {
					var dump bytes.Buffer
					if err := stuntgres.Dump(p.ctx, p.native, &dump); err != nil {
						t.Fatal(err)
					}
					nativeDump = dump.String()
					if err := stuntgres.Reset(p.ctx, p.native); err != nil {
						t.Fatal(err)
					}
					if err := stuntgres.Restore(p.ctx, p.native, strings.NewReader(nativeDump)); err != nil {
						t.Fatal("actual native Dump cannot restore", err)
					}
					check(b)
				}
				consume(b)
			})
			if nativeDump != "" && p.postgres != nil {
				b := p.backends()[1]
				pgDependExec(t, p, b, "DROP SCHEMA "+schema+" CASCADE")
				if _, err := b.db.ExecContext(p.ctx, nativeDump); err != nil {
					t.Fatal("pinned PG rejected actual native Dump", err)
				}
				check(b)
				consume(b)
			}
		})
	}
}
