package reference_test

import (
	"database/sql"
	"testing"

	"stuntgres/reference/fixture"
)

// TestReferenceExprArraySubqueryDML replays the coder checker repro. Every
// route reseeds the unchanged starting rows first, so a direct and a prepared
// call never observe (or apply) the other route's effect.
func TestReferenceExprArraySubqueryDML(t *testing.T) {
	arraySubqueryScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
		run := arraySubqueryRun(t, p, b, c)
		run(fixture.Exec("CREATE TABLE arrq_t(id int, scopes text[], ints int[])", 0))
		run(fixture.Exec("INSERT INTO arrq_t VALUES (1, ARRAY['a','x','b'], NULL)", 1))
		run(fixture.Exec("CREATE TABLE arrq_m(id int, scopes text[])", 0))
		run(fixture.Exec("INSERT INTO arrq_m VALUES (1, ARRAY['x','a']), (2, ARRAY['b','c'])", 2))

		for _, prepared := range []bool{false, true} {
			name := "direct"
			if prepared {
				name = "prepared"
			}
			t.Run(name, func(t *testing.T) {
				run := arraySubqueryRun(t, p, b, c)
				apply := func(s fixture.Step) fixture.Step {
					if prepared {
						return fixture.Prepared(s)
					}
					return s
				}
				run(fixture.Exec("UPDATE arrq_t SET scopes=ARRAY['a','x','b'], ints=NULL WHERE id=1", 1))
				run(fixture.Exec("DELETE FROM arrq_m", 2))
				run(fixture.Exec("INSERT INTO arrq_m VALUES (1, ARRAY['x','a']), (2, ARRAY['b','c'])", 2))

				// Checker repro: filter each row's own scopes through unnest.
				run(apply(fixture.Exec("UPDATE arrq_t SET scopes = ARRAY(SELECT s FROM unnest(scopes) s WHERE s <> 'x')", 1)))
				run(fixture.ArrayReferenceSelect("SELECT scopes FROM arrq_t", []string{"scopes"}, []string{"_TEXT"}, []string{"_TEXT"}, [][]any{{"{a,b}"}}, b.PG))
				run(apply(fixture.Exec("UPDATE arrq_t SET scopes = ARRAY(SELECT s FROM unnest(scopes) s WHERE false)", 1)))
				run(fixture.ArrayReferenceSelect("SELECT scopes FROM arrq_t", []string{"scopes"}, []string{"_TEXT"}, []string{"_TEXT"}, [][]any{{"{}"}}, b.PG))

				run(fixture.Exec("UPDATE arrq_t SET scopes=ARRAY['a','b'] WHERE id=1", 1))
				run(apply(fixture.Exec("UPDATE arrq_t SET ints = ARRAY(SELECT 1)", 1)))
				run(fixture.ArrayReferenceSelect("SELECT ints FROM arrq_t", []string{"ints"}, []string{"_INT4"}, []string{"_INT4"}, [][]any{{"{1}"}}, b.PG))
				run(apply(fixture.Exec("UPDATE arrq_t SET scopes = ARRAY(SELECT 2)", 1)))
				run(fixture.ArrayReferenceSelect("SELECT scopes FROM arrq_t", []string{"scopes"}, []string{"_TEXT"}, []string{"_TEXT"}, [][]any{{"{2}"}}, b.PG))

				// Multi-row correlation: each row filters its own scopes.
				run(apply(fixture.Exec("UPDATE arrq_m SET scopes = ARRAY(SELECT s FROM unnest(scopes) s WHERE s <> 'x')", 2)))
				run(fixture.ArrayReferenceSelect("SELECT id, scopes FROM arrq_m ORDER BY id", []string{"id", "scopes"}, []string{"INT4", "_TEXT"}, []string{"INT4", "_TEXT"}, [][]any{{int64(1), "{a}"}, {int64(2), "{b,c}"}}, b.PG))
			})
		}
	})
}
