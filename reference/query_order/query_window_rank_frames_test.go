package reference_test

import (
	"fmt"
	"testing"

	"stuntgres/reference/fixture"
)

func queryWindowRankExcluded(t *testing.T, p *fixture.Pair, b fixture.Backend) {
	t.Helper()
	over := "(PARTITION BY p ORDER BY k ROWS CURRENT ROW EXCLUDE GROUP)"
	rows := queryWindowRankRows()
	for i := range rows {
		rows[i] = append(rows[i], int64(0))
	}
	queryWindowRoutes(t, p, b, fixture.Select("SELECT id,"+queryWindowRankProjection(over)+",count(*) OVER "+over+" AS n FROM t ORDER BY id", []string{"id", "r", "d", "pr", "cd", "n"}, []string{"INT4", "INT8", "INT8", "FLOAT8", "FLOAT8", "INT8"}, rows))
}

func TestReferenceQueryWindowRankFrames(t *testing.T) {
	queryWindowRankScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend) {
		queryWindowSeed(t, p, b)
		t.Run("membership-sibling", func(t *testing.T) { queryWindowRankExcluded(t, p, b) })
		for _, frame := range []string{"ROWS CURRENT ROW", "RANGE CURRENT ROW EXCLUDE TIES", "GROUPS BETWEEN 1 PRECEDING AND CURRENT ROW EXCLUDE CURRENT ROW"} {
			t.Run(frame, func(t *testing.T) {
				queryWindowRoutes(t, p, b, queryWindowRankResult("SELECT id,"+queryWindowRankProjection("(PARTITION BY p ORDER BY k "+frame+")")+" FROM t ORDER BY id", queryWindowRankRows()))
			})
		}
		// Ranking-only windows eliminate runtime bounds, including NULL and
		// negative offsets; a frame-sensitive sibling still demands validation.
		for _, f := range []string{"rank", "dense_rank", "percent_rank", "cume_dist"} {
			for _, n := range []any{nil, -1} {
				t.Run(fmt.Sprintf("eliminated-%s-%v", f, n), func(t *testing.T) {
					typ, value := "INT8", any(int64(1))
					if f == "percent_rank" {
						typ, value = "FLOAT8", float64(0)
					}
					if f == "cume_dist" {
						typ, value = "FLOAT8", float64(1)
					}
					for _, suffix := range []string{"", " WHERE FALSE"} {
						var rows [][]any
						if suffix == "" {
							rows = [][]any{{value}}
						}
						queryWindowRoutes(t, p, b, fixture.Select("SELECT "+f+"() OVER(ORDER BY 1 ROWS $1 PRECEDING)"+suffix, []string{f}, []string{typ}, rows, n))
					}
				})
			}
		}
		for _, tc := range []struct {
			n             any
			code, message string
		}{{nil, "22004", "frame starting offset must not be null"}, {-1, "22013", "frame starting offset must not be negative"}} {
			t.Run("retained-"+tc.code, func(t *testing.T) {
				queryWindowValueDiagnosticRoutes(t, p, b, "SELECT "+queryWindowRankProjection("w")+",count(*) OVER w FROM t WINDOW w AS(ORDER BY k ROWS $1 PRECEDING)", fixture.Diagnostic{Code: tc.code, Message: tc.message}, "execute", tc.n)
			})
		}
	})
}
