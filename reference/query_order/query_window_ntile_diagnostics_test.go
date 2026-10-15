package reference_test

import (
	"fmt"
	"testing"

	"stuntgres/reference/fixture"
)

func TestReferenceQueryWindowNtileDiagnostics(t *testing.T) {
	queryWindowValueScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend) {
		queryWindowNtileSeed(t, p, b)
		const hint = "No function matches the given name and argument types. You might need to add explicit type casts."
		// Independent release-derived contracts, checked against pinned PG18.
		for i, tc := range []struct {
			q, code, message string
			pos              int
			hint, stage      string
		}{
			{"SELECT ntile(0) OVER()", "22014", "argument of ntile must be greater than zero", 0, "", "execute"},
			{"SELECT ntile(-1) OVER()", "22014", "argument of ntile must be greater than zero", 0, "", "execute"},
			{"SELECT ntile(2)", "42809", "window function ntile requires an OVER clause", 8, "", "prepare"},
			{"SELECT pg_catalog.ntile(2)", "42809", "window function pg_catalog.ntile requires an OVER clause", 8, "", "prepare"},
			{"SELECT ntile() OVER()", "42883", "function ntile() does not exist", 8, hint, "prepare"},
			{"SELECT ntile(1,2) OVER()", "42883", "function ntile(integer, integer) does not exist", 8, hint, "prepare"},
			{"SELECT ntile(1::bigint) OVER()", "42883", "function ntile(bigint) does not exist", 8, hint, "prepare"},
			{"SELECT ntile('2'::text) OVER()", "42883", "function ntile(text) does not exist", 8, hint, "prepare"},
			{"SELECT ntile(TRUE) OVER()", "42883", "function ntile(boolean) does not exist", 8, hint, "prepare"},
			{"SELECT ntile(1.5) OVER()", "42883", "function ntile(numeric) does not exist", 8, hint, "prepare"},
			{"SELECT ntile('bad') OVER()", "22P02", `invalid input syntax for type integer: "bad"`, 14, "", "prepare"},
			{"SELECT 1 WHERE ntile(2) OVER()>0", "42P20", "window functions are not allowed in WHERE", 16, "", "prepare"},
			{"SELECT 1 WHERE ntile(TRUE) OVER()>0", "42883", "function ntile(boolean) does not exist", 16, hint, "prepare"},
			{"SELECT ntile(missing) OVER()", "42703", `column "missing" does not exist`, 14, "", "prepare"},
			{"SELECT ntile(2) FILTER(WHERE missing) OVER()", "42703", `column "missing" does not exist`, 30, "", "prepare"},
			{"SELECT ntile(2) FILTER(WHERE TRUE) OVER()", "0A000", "FILTER is not implemented for non-aggregate window functions", 8, "", "prepare"},
			{"SELECT ntile(DISTINCT 2) OVER()", "0A000", "DISTINCT is not implemented for window functions", 8, "", "prepare"},
			{"SELECT ntile(2 ORDER BY missing) OVER()", "0A000", "aggregate ORDER BY is not implemented for window functions", 8, "", "prepare"},
			{"SELECT ntile(ntile(2) OVER()) OVER()", "42P20", "window function calls cannot be nested", 14, "", "prepare"},
			{"SELECT ntile(ntile(2) OVER(),1) OVER()", "42883", "function ntile(integer, integer) does not exist", 8, hint, "prepare"},
			{"SELECT ntile(ntile(2) OVER()) FILTER(WHERE TRUE) OVER()", "0A000", "FILTER is not implemented for non-aggregate window functions", 8, "", "prepare"},
			{"SELECT pg_catalog.ntile(TRUE)", "42883", "function pg_catalog.ntile(boolean) does not exist", 8, hint, "prepare"},
			{"SELECT ntile()", "42883", "function ntile() does not exist", 8, hint, "prepare"},
			{"SELECT ntile(2) OVER(ROWS UNBOUNDED FOLLOWING)", "42P20", "frame start cannot be UNBOUNDED FOLLOWING", 27, "", "prepare"},
			{"SELECT ntile(2) OVER(GROUPS CURRENT ROW)", "42P20", "GROUPS mode requires an ORDER BY clause", 21, "", "prepare"},
			{"SELECT ntile(2) OVER(ORDER BY id ROWS id PRECEDING) FROM nt", "42P10", "argument of ROWS must not contain variables", 39, "", "prepare"},
			{"SELECT ntile(2) OVER(ORDER BY id ROWS TRUE PRECEDING) FROM nt", "42804", "argument of ROWS must be type bigint, not type boolean", 39, "", "prepare"},
		} {
			t.Run(fmt.Sprintf("case-%d", i), func(t *testing.T) {
				for _, prefix := range []string{"", "/*é*/ "} {
					t.Run(fmt.Sprintf("prefix=%q", prefix), func(t *testing.T) {
						pos := tc.pos
						if pos != 0 {
							pos += len([]rune(prefix))
						}
						queryWindowValueDiagnosticRoutes(t, p, b, prefix+tc.q, fixture.Diagnostic{Code: tc.code, Message: tc.message, Position: fixture.Position(pos), Hint: tc.hint}, tc.stage)
					})
				}
			})
		}
		for _, tc := range []struct{ arg, code, message string }{
			{"bad", "22P02", `invalid input syntax for type integer: "bad"`},
			{"2147483648", "22003", `value "2147483648" is out of range for type integer`},
		} {
			t.Run("parameter-"+tc.code, func(t *testing.T) {
				queryWindowValueDiagnosticRoutes(t, p, b, "SELECT ntile($1) OVER()", fixture.Diagnostic{Code: tc.code, Message: tc.message, Context: "unnamed portal parameter $1 = '...'"}, "execute", tc.arg)
			})
		}
		for _, arg := range []any{2, "2", nil} {
			value := any(int64(1))
			if arg == nil {
				value = nil
			}
			queryWindowRoutes(t, p, b, fixture.Select("SELECT ntile($1) OVER() AS n", []string{"n"}, []string{"INT4"}, [][]any{{value}}, arg))
		}
	})
}
