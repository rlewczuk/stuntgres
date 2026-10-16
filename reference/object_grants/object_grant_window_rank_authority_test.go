package reference_test

import (
	"database/sql"
	"testing"

	"stuntgres/reference/fixture"
)

func TestReferenceObjectGrantWindowRankProviderAuthority(t *testing.T) {
	for _, tc := range []struct {
		name, typ, cast string
		first, second   any
		fallback        any
	}{
		{"rank", "INT8", "BIGINT", int64(1), int64(2), int64(7)},
		{"dense_rank", "INT8", "BIGINT", int64(1), int64(2), int64(7)},
		{"percent_rank", "FLOAT8", "DOUBLE PRECISION", float64(0), float64(1), float64(7)},
		{"cume_dist", "FLOAT8", "DOUBLE PRECISION", float64(.5), float64(1), float64(7)},
	} {
		t.Run(tc.name, func(t *testing.T) {
			objectGrantScenario(t, nil, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn, run func(fixture.Step), prepared bool) {
				run(fixture.Exec("GRANT USAGE ON SCHEMA public TO og_object_reader", 0))
				run(fixture.Exec("CREATE TABLE og_window_rank_source(id INT)", 0))
				run(fixture.Exec("INSERT INTO og_window_rank_source VALUES(1),(2)", 2))
				run(fixture.Exec("GRANT SELECT ON og_window_rank_source TO og_object_reader", 0))
				vector := func(expr, tail string, values []any) fixture.Step {
					var rows [][]any
					for i, value := range values {
						rows = append(rows, []any{int64(i + 1), value})
					}
					return fixture.Select("SELECT id,"+expr+" AS v FROM og_window_rank_source"+tail+" ORDER BY id", []string{"id", "v"}, []string{"INT4", tc.typ}, rows)
				}
				call := "pg_catalog." + tc.name + "() OVER(ORDER BY id)"
				cases := []objectGrantProviderCase{
					{vector(call, "", []any{tc.first, tc.second}), true, true},
					// A row-dependent nullable projection retains its window.
					{vector("CASE WHEN id=1 THEN NULL::"+tc.cast+" ELSE "+call+" END", "", []any{nil, tc.second}), true, true},
					// Surviving windows initialize even with no input rows.
					{vector(call, " WHERE id>99", nil), true, true},
					{vector(call, " WHERE FALSE", nil), true, true},
					// Constant-dead window arms are pruned, unlike empty input.
					{vector("CASE WHEN FALSE THEN "+call+" ELSE 7::"+tc.cast+" END", "", []any{tc.fallback, tc.fallback}), false, true},
					{vector("CASE WHEN FALSE THEN "+call+" ELSE NULL::"+tc.cast+" END", "", []any{nil, nil}), false, true},
				}
				controls := []fixture.Step{
					fixture.Select("SELECT id,pg_catalog.row_number() OVER(ORDER BY id) AS v FROM og_window_rank_source ORDER BY id", []string{"id", "v"}, []string{"INT4", "INT8"}, [][]any{{int64(1), int64(1)}, {int64(2), int64(2)}}),
				}
				objectGrantProviderLifecycle(t, p, b, c, run, "pg_catalog."+tc.name+"()", "function", tc.name, cases, controls)
			})
		})
	}
}
