package reference_test

import (
	"fmt"
	"strings"
	"testing"

	"stuntgres/reference/fixture"
)

func queryWindowRankDiagnostic(t *testing.T, p *fixture.Pair, b fixture.Backend, q, code, message, token string) {
	t.Helper()
	for _, prefix := range []string{"", "/*é*/ "} {
		t.Run(fmt.Sprintf("prefix=%q", prefix), func(t *testing.T) {
			pos := 0
			if token != "" {
				pos = len([]rune(prefix+q[:strings.Index(q, token)])) + 1
			}
			queryWindowValueDiagnosticRoutes(t, p, b, prefix+q, fixture.Diagnostic{Code: code, Message: message, Position: fixture.Position(pos)}, "prepare")
		})
	}
}

func queryWindowRankBasicDiagnostics(t *testing.T, p *fixture.Pair, b fixture.Backend) {
	t.Helper()
	queryWindowRankDiagnostic(t, p, b, "SELECT rank()", "42809", "window function rank requires an OVER clause", "rank")
	queryWindowRankDiagnostic(t, p, b, "SELECT rank(1) OVER()", "42809", "WITHIN GROUP is required for ordered-set aggregate rank", "rank")
}

func TestReferenceQueryWindowRankDiagnostics(t *testing.T) {
	queryWindowRankScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend) {
		for _, name := range []string{"rank", "dense_rank", "percent_rank", "cume_dist"} {
			for _, qualifier := range []string{"", "pg_catalog."} {
				f := qualifier + name
				t.Run(f, func(t *testing.T) {
					t.Run("missing-over", func(t *testing.T) {
						queryWindowRankDiagnostic(t, p, b, "SELECT "+f+"()", "42809", "window function "+f+" requires an OVER clause", f)
					})
					for _, args := range []string{"1", "1,2"} {
						for _, over := range []string{"", " OVER()"} {
							t.Run("arity-"+args+over, func(t *testing.T) {
								queryWindowRankDiagnostic(t, p, b, "SELECT "+f+"("+args+")"+over, "42809", "WITHIN GROUP is required for ordered-set aggregate "+f, f)
							})
						}
					}
					// PostgreSQL accepts star for these zero-argument window
					// overloads, unlike the ordinary row_number signature helper.
					t.Run("star-over", func(t *testing.T) {
						typ, v := "INT8", any(int64(1))
						if name == "percent_rank" {
							typ, v = "FLOAT8", float64(0)
						}
						if name == "cume_dist" {
							typ, v = "FLOAT8", float64(1)
						}
						queryWindowRoutes(t, p, b, fixture.Select("SELECT "+f+"(*) OVER()", []string{name}, []string{typ}, [][]any{{v}}))
					})
					t.Run("star-missing-over", func(t *testing.T) {
						queryWindowRankDiagnostic(t, p, b, "SELECT "+f+"(*)", "42809", "window function "+f+" requires an OVER clause", f)
					})
				})
			}
			for _, tc := range []struct{ q, code, message, token string }{
				{"SELECT " + name + "(missing) OVER()", "42703", `column "missing" does not exist`, "missing"},
				{"SELECT " + name + "(1) WITHIN GROUP(ORDER BY 1) OVER()", "0A000", "OVER is not supported for ordered-set aggregate " + name, name},
				{"SELECT " + name + "() FILTER(WHERE TRUE) OVER()", "0A000", "FILTER is not implemented for non-aggregate window functions", name},
				{"SELECT " + name + "() FILTER(WHERE missing) OVER()", "42703", `column "missing" does not exist`, "missing"},
				{"SELECT 1 WHERE " + name + "() OVER()>0", "42P20", "window functions are not allowed in WHERE", name},
				{"SELECT count(" + name + "() OVER())", "42803", "aggregate function calls cannot contain window function calls", name},
				{"SELECT " + name + "() OVER missing", "42704", `window "missing" does not exist`, "missing"},
			} {
				t.Run(tc.q, func(t *testing.T) { queryWindowRankDiagnostic(t, p, b, tc.q, tc.code, tc.message, tc.token) })
			}
		}
	})
}
