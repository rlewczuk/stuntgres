package reference_test

import (
	"database/sql"
	"testing"

	"stuntgres/reference/fixture"
)

// TestReferenceExprArraySubqueryDiagnostics records the PostgreSQL 18.6
// diagnostics: the single-column check and the missing array type fail while
// binding (prepare for the prepared route), while array accumulation fails
// during execution. Multi-column is 42601, not the 21000 the catalog text
// claimed.
func TestReferenceExprArraySubqueryDiagnostics(t *testing.T) {
	arraySubqueryScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
		bindCases := []struct {
			name     string
			query    string
			code     string
			message  string
			position int
		}{
			{"multi_column", "SELECT ARRAY(SELECT 1,2)", "42601", "subquery must return only one column", 8},
			{"multi_column_utf8", "SELECT /*é*/ ARRAY(SELECT 1,2)", "42601", "subquery must return only one column", 14},
			{"void_element", "SELECT ARRAY(SELECT pg_sleep(0))", "42704", "could not find array type for data type void", 0},
		}
		for _, tc := range bindCases {
			t.Run(tc.name, func(t *testing.T) {
				run := arraySubqueryRun(t, p, b, c)
				want := fixture.ControlsReferenceDiagnostic(tc.code, tc.message)
				want.Position = fixture.Position(tc.position)
				for _, prepared := range []bool{false, true} {
					for _, queryMode := range []bool{false, true} {
						s := fixture.ErrorDiagnostic(tc.query, want)
						s.SelectRows, s.Prepared = queryMode, prepared
						if prepared {
							s.ErrorStage = "prepare"
						} else {
							s.ErrorStage = "execute"
						}
						run(s)
					}
				}
			})
		}
		executeCases := []struct{ name, query, code, message string }{
			{"null_array_row", "SELECT ARRAY(SELECT NULL::int[])", "22004", "cannot accumulate null arrays"},
			{"empty_array_row", "SELECT ARRAY(SELECT '{}'::int[])", "2202E", "cannot accumulate empty arrays"},
			{"different_dimensionality", "SELECT ARRAY(SELECT x FROM (VALUES ('{1,2}'::int[]),('{1,2,3}'::int[])) v(x))", "2202E", "cannot accumulate arrays of different dimensionality"},
		}
		for _, tc := range executeCases {
			t.Run(tc.name, func(t *testing.T) {
				run := arraySubqueryRun(t, p, b, c)
				want := fixture.ControlsReferenceDiagnostic(tc.code, tc.message)
				for _, prepared := range []bool{false, true} {
					s := fixture.ErrorDiagnostic(tc.query, want)
					s.SelectRows, s.Prepared = true, prepared
					s.ErrorStage = "execute"
					run(s)
				}
			})
		}
	})
}
