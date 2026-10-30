package reference_test

import (
	"context"
	"database/sql"
	"strings"
	"testing"

	"stuntgres/reference/fixture"
)

func TestReferenceFDWPushdownFeatureSmoke(t *testing.T) {
	fdwScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn, r *fdwSQLReader, run func(fixture.Step)) {
		fdwPushdownLive(t, p, b, r, run)
		seen := make(map[string]bool)
		r.setHook(func(ctx context.Context, method string) error {
			if strings.HasPrefix(method, "ExecuteForeignQueryStage:") {
				seen[strings.TrimPrefix(method, "ExecuteForeignQueryStage:")] = true
			}
			return ctx.Err()
		})
		defer r.setHook(nil)
		inner := [][]any{{int64(1), int64(10), int64(100)}, {int64(1), int64(10), int64(100)}, {int64(1), nil, int64(100)}, {int64(1), nil, int64(100)}}
		left := append(append([][]any{}, inner...), []any{int64(2), int64(20), nil}, []any{nil, int64(30), nil}, []any{int64(4), int64(40), nil})
		full := append(append([][]any{}, left...), []any{int64(3), nil, int64(300)}, []any{nil, nil, int64(900)})
		for _, tc := range []struct {
			join, key string
			rows      [][]any
		}{{"INNER", "l.k", inner}, {"LEFT", "l.k", left}, {"FULL", "COALESCE(l.k,r.k)", full}} {
			fdwPushdownValue(run, "SELECT "+tc.key+" AS k,l.v,r.w FROM fdw_push_left l "+tc.join+" JOIN fdw_push_right r ON l.k=r.k", []string{"k", "v", "w"}, []string{"INT4", "INT4", "INT4"}, tc.rows, true)
		}
		fdwPushdownValue(run, "SELECT l.k,count(*) AS n,sum(l.v) AS v,sum(r.w) AS w FROM fdw_push_left l JOIN fdw_push_right r ON l.k=r.k GROUP BY l.k", []string{"k", "n", "v", "w"}, []string{"INT4", "INT8", "INT8", "INT8"}, [][]any{{int64(1), int64(4), int64(20), int64(400)}}, false)
		fdwPushdownValue(run, "SELECT k,v FROM fdw_push_left ORDER BY k NULLS LAST,v NULLS LAST OFFSET 1 LIMIT 2", []string{"k", "v"}, []string{"INT4", "INT4"}, [][]any{{int64(1), nil}, {int64(2), int64(20)}}, false)
		fdwPushdownValue(run, "SELECT l.k,count(*) AS n FROM fdw_push_left l JOIN fdw_push_right r ON l.k=r.k GROUP BY l.k ORDER BY l.k NULLS LAST LIMIT 1", []string{"k", "n"}, []string{"INT4", "INT8"}, [][]any{{int64(1), int64(4)}}, false)
		fdwPushdownValue(run, "SELECT l.k,r.w FROM fdw_push_left l LEFT JOIN fdw_push_right r ON l.k=r.k AND l.v+0<0", []string{"k", "w"}, []string{"INT4", "INT4"}, [][]any{{int64(1), nil}, {int64(1), nil}, {int64(2), nil}, {nil, nil}, {int64(4), nil}}, true)
		if !b.PG {
			for _, kind := range []string{"join", "aggregate", "order", "limit"} {
				if !seen[kind] {
					t.Errorf("real provider did not execute %s stage", kind)
				}
			}
		}
		r.setHook(nil)
		if !b.PG {
			run(fixture.Exec("ALTER FOREIGN DATA WRAPPER fdw_native HANDLER fdw_scan_only_handler", 0))
		}
		fdwPushdownValue(run, "SELECT count(*) FROM fdw_push_left l JOIN fdw_push_right r ON l.k=r.k", []string{"count"}, []string{"INT8"}, [][]any{{int64(4)}}, false)
	})
}
