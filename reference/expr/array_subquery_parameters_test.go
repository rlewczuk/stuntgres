package reference_test

import (
	"database/sql"
	"testing"

	"stuntgres/reference/fixture"
)

// TestReferenceExprArraySubqueryParameters pins the extended-protocol contract:
// untyped parameters resolve to text, typed arithmetic keeps its type, series
// bounds and text[] UPDATE parameters bind, and the prepared result type is
// reported as integer[].
func TestReferenceExprArraySubqueryParameters(t *testing.T) {
	arraySubqueryScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
		cases := []struct {
			name  string
			query string
			names []string
			types []string
			rows  [][]any
			args  []any
		}{
			{"unknown_parameter", "SELECT ARRAY(SELECT $1)", []string{"array"}, []string{"_TEXT"}, [][]any{{"{abc}"}}, []any{"abc"}},
			{"typed_arithmetic", "SELECT ARRAY(SELECT $1::int + g FROM generate_series(1,2) g) AS a", []string{"a"}, []string{"_INT4"}, [][]any{{"{11,12}"}}, []any{int64(10)}},
			{"series_bound", "SELECT ARRAY(SELECT g FROM generate_series(1,$1::int) g) AS a", []string{"a"}, []string{"_INT4"}, [][]any{{"{1,2,3}"}}, []any{int64(3)}},
		}
		for _, tc := range cases {
			t.Run(tc.name, func(t *testing.T) {
				run := arraySubqueryRun(t, p, b, c)
				arraySubqueryBoth(run, fixture.ArrayReferenceSelect(tc.query, tc.names, tc.types, tc.types, tc.rows, b.PG, tc.args...))
			})
		}

		run := arraySubqueryRun(t, p, b, c)
		run(fixture.Exec("CREATE TABLE arrq_u(id int, scopes text[])", 0))
		run(fixture.Exec("INSERT INTO arrq_u VALUES (1, ARRAY['s'])", 1))
		for _, prepared := range []bool{false, true} {
			name := "direct"
			if prepared {
				name = "prepared"
			}
			t.Run("update_set/"+name, func(t *testing.T) {
				run := arraySubqueryRun(t, p, b, c)
				run(fixture.Exec("UPDATE arrq_u SET scopes=ARRAY['s'] WHERE id=1", 1))
				s := fixture.Exec("UPDATE arrq_u SET scopes = ARRAY(SELECT s FROM unnest($1::text[]) s WHERE s <> 'x')", 1, "{p,x,q}")
				if prepared {
					s = fixture.Prepared(s)
				}
				run(s)
				run(fixture.ArrayReferenceSelect("SELECT scopes FROM arrq_u", []string{"scopes"}, []string{"_TEXT"}, []string{"_TEXT"}, [][]any{{"{p,q}"}}, b.PG))
			})
		}

		t.Run("prepared_result_type", func(t *testing.T) {
			run := arraySubqueryRun(t, p, b, c)
			run(fixture.Exec("PREPARE arrq_prep AS SELECT ARRAY(SELECT g FROM generate_series(1,3) g)", 0))
			run(fixture.Select("SELECT result_types::text AS result_types FROM pg_prepared_statements WHERE name='arrq_prep'", []string{"result_types"}, []string{"TEXT"}, [][]any{{"{integer[]}"}}))
			run(fixture.Exec("DEALLOCATE arrq_prep", 0))
		})
	})
}
