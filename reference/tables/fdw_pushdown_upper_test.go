package reference_test

import (
	"database/sql"
	"testing"

	"stuntgres"
	"stuntgres/reference/fixture"
)

func TestReferenceFDWPushdownUpper(t *testing.T) {
	fdwScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn, r *fdwSQLReader, run func(fixture.Step)) {
		fdwPushdownLive(t, p, b, r, run)
		observed := fdwPushdownObserve(r)
		defer observed(t, b.PG, "GetForeignUpperPaths")
		grouped, global := false, false
		var inspect func(stuntgres.ForeignQueryStage)
		inspect = func(stage stuntgres.ForeignQueryStage) {
			if stage.Kind == "aggregate" {
				width := len(stage.Keys) + len(stage.Aggregates)
				if len(stage.Slots) != width+1 || len(stage.Outputs) != width+1 {
					t.Fatal("aggregate omitted immutable frame cells", stage)
				}
				hidden := stage.Outputs[width]
				if hidden.Kind != "literal" || hidden.Type.TypeOID != 23 || hidden.Literal != int64(0) {
					t.Fatal("invalid hidden grouping occurrence", hidden)
				}
				if len(stage.Keys) == 0 {
					global = true
				} else {
					grouped = true
				}
			}
			for _, child := range stage.Children {
				inspect(child)
			}
		}
		r.mu.Lock()
		r.queryRequestHook = func(request stuntgres.ForeignQueryRequest) { inspect(request.Stage) }
		r.mu.Unlock()
		defer func() {
			r.mu.Lock()
			r.queryRequestHook = nil
			r.mu.Unlock()
			if !b.PG && (!grouped || !global) {
				t.Error("real SQL provider did not execute grouped and global aggregates", grouped, global)
			}
		}()
		fdwPushdownValue(run, "SELECT k,count(*),count(v),sum(v) FROM fdw_push_left GROUP BY k", []string{"k", "count", "count", "sum"}, []string{"INT4", "INT8", "INT8", "INT8"}, [][]any{{int64(1), int64(2), int64(1), int64(10)}, {int64(2), int64(1), int64(1), int64(20)}, {int64(4), int64(1), int64(1), int64(40)}, {nil, int64(1), int64(1), int64(30)}}, true)
		fdwPushdownValue(run, "SELECT l.k,count(*),count(l.v),sum(l.v),sum(r.w) FROM fdw_push_left l JOIN fdw_push_right r ON l.k=r.k GROUP BY l.k", []string{"k", "count", "count", "sum", "sum"}, []string{"INT4", "INT8", "INT8", "INT8", "INT8"}, [][]any{{int64(1), int64(4), int64(2), int64(20), int64(400)}}, true)
		fdwPushdownValue(run, "SELECT count(*),sum(v) FROM fdw_push_left WHERE k=99", []string{"count", "sum"}, []string{"INT8", "INT8"}, [][]any{{int64(0), nil}}, false)
		fdwPushdownValue(run, "SELECT k,count(*),sum(v) FROM fdw_push_left WHERE k=99 GROUP BY k", []string{"k", "count", "sum"}, []string{"INT4", "INT8", "INT8"}, nil, true)
		fdwPushdownValue(run, "SELECT k,count(*) FROM fdw_push_left GROUP BY k HAVING count(*)>1", []string{"k", "count"}, []string{"INT4", "INT8"}, [][]any{{int64(1), int64(2)}}, true)
		// The HAVING aggregate is required internally, but is not a public column.
		fdwPushdownValue(run, "SELECT k FROM fdw_push_left GROUP BY k HAVING count(*)>1", []string{"k"}, []string{"INT4"}, [][]any{{int64(1)}}, true)
		fdwPushdownValue(run, "SELECT k,count(*) FROM fdw_push_left WHERE v IS NOT NULL GROUP BY k", []string{"k", "count"}, []string{"INT4", "INT8"}, [][]any{{int64(1), int64(1)}, {int64(2), int64(1)}, {int64(4), int64(1)}, {nil, int64(1)}}, true)

		columns, types := []string{"k", "v"}, []string{"INT4", "INT4"}
		all := [][]any{{int64(1), int64(10)}, {int64(1), nil}, {int64(2), int64(20)}, {int64(4), int64(40)}, {nil, int64(30)}}
		ordered := "SELECT k,v FROM fdw_push_left ORDER BY k ASC NULLS LAST,v ASC NULLS LAST"
		fdwPushdownValue(run, ordered+" OFFSET 1 LIMIT 2", columns, types, [][]any{{int64(1), nil}, {int64(2), int64(20)}}, false)
		fdwPushdownValue(run, "SELECT k,v FROM fdw_push_left ORDER BY k DESC NULLS FIRST,v DESC NULLS FIRST OFFSET 1 LIMIT 3", columns, types, [][]any{{int64(4), int64(40)}, {int64(2), int64(20)}, {int64(1), nil}}, false)
		fdwPushdownValue(run, ordered+" LIMIT 0", columns, types, nil, false)
		fdwPushdownValue(run, ordered+" LIMIT NULL OFFSET NULL", columns, types, all, false)
		fdwPushdownValue(run, ordered+" LIMIT ALL", columns, types, all, false)
		fdwPushdownValue(run, ordered+" OFFSET 9223372036854775807", columns, types, nil, false)
		fdwPushdownValue(run, "SELECT l.k,count(*) AS n FROM fdw_push_left l JOIN fdw_push_right r ON l.k=r.k GROUP BY l.k ORDER BY l.k ASC NULLS LAST LIMIT 1", []string{"k", "n"}, []string{"INT4", "INT8"}, [][]any{{int64(1), int64(4)}}, false)
		fdwPushdownValue(run, "SELECT k FROM fdw_push_left GROUP BY k HAVING count(*)>1 ORDER BY k ASC NULLS LAST LIMIT 1", []string{"k"}, []string{"INT4"}, [][]any{{int64(1)}}, false)

		query := ordered + " OFFSET $1 LIMIT $2"
		fdwPushdownHeld(t, p, b, c, query,
			fixture.Select(query, columns, types, [][]any{{int64(1), nil}, {int64(2), int64(20)}}, int64(1), int64(2)),
			fixture.Select(query, columns, types, [][]any{{int64(4), int64(40)}, {nil, int64(30)}}, int64(3), int64(2)),
			fixture.Select(query, columns, types, all, nil, nil),
			fixture.Select(query, columns, types, nil, int64(0), int64(0)))
		// Bound errors are ordinary local diagnostics, including on empty input;
		// the provider may not reinterpret negative bounds as an unbounded query.
		for _, prepared := range []bool{false, true} {
			for _, tc := range []struct {
				query, code, message string
			}{
				{"SELECT k,v FROM fdw_push_left WHERE k=99 LIMIT $1", "2201W", "LIMIT must not be negative"},
				{"SELECT k,v FROM fdw_push_left WHERE k=99 OFFSET $1", "2201X", "OFFSET must not be negative"},
			} {
				s := fixture.ErrorDiagnostic(tc.query, fdwDiagnostic(tc.code, tc.message), int64(-1))
				pg := fdwDiagnostic(tc.code, tc.message)
				if tc.code == "2201W" {
					pg.Context = `remote SQL command: SELECT "Key", "Value" FROM fdw_source."Left input" WHERE (("Key" = 99)) LIMIT (-1)::bigint`
				}
				s.PostgresDiagnostic = &pg
				s.Prepared = prepared
				run(s)
			}
		}
	})
}
