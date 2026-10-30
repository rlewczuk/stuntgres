package reference_test

import (
	"database/sql"
	"testing"

	"stuntgres/reference/fixture"
)

func TestReferenceFDWPushdownJoins(t *testing.T) {
	fdwScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn, r *fdwSQLReader, run func(fixture.Step)) {
		fdwPushdownLive(t, p, b, r, run)
		observed := fdwPushdownObserve(r)
		defer observed(t, b.PG, "GetForeignJoinPaths")
		columns := []string{"k", "v", "w"}
		types := []string{"INT4", "INT4", "INT4"}
		inner := [][]any{{int64(1), int64(10), int64(100)}, {int64(1), int64(10), int64(100)}, {int64(1), nil, int64(100)}, {int64(1), nil, int64(100)}}
		left := [][]any{{int64(1), int64(10), int64(100)}, {int64(1), int64(10), int64(100)}, {int64(1), nil, int64(100)}, {int64(1), nil, int64(100)}, {int64(2), int64(20), nil}, {nil, int64(30), nil}, {int64(4), int64(40), nil}}
		right := [][]any{{int64(1), int64(10), int64(100)}, {int64(1), int64(10), int64(100)}, {int64(1), nil, int64(100)}, {int64(1), nil, int64(100)}, {int64(3), nil, int64(300)}, {nil, nil, int64(900)}}
		full := [][]any{{int64(1), int64(10), int64(100)}, {int64(1), int64(10), int64(100)}, {int64(1), nil, int64(100)}, {int64(1), nil, int64(100)}, {int64(2), int64(20), nil}, {nil, int64(30), nil}, {int64(4), int64(40), nil}, {int64(3), nil, int64(300)}, {nil, nil, int64(900)}}
		for _, tc := range []struct {
			kind, key string
			rows      [][]any
		}{
			{"INNER", "l.k", inner},
			{"LEFT", "l.k", left},
			{"RIGHT", "r.k", right},
			{"FULL", "COALESCE(l.k,r.k)", full},
		} {
			fdwPushdownValue(run, "SELECT "+tc.key+" AS k,l.v,r.w FROM fdw_push_left l "+tc.kind+" JOIN fdw_push_right r ON l.k=r.k", columns, types, tc.rows, true)
		}
		fdwPushdownValue(run, "SELECT count(*) FROM fdw_push_left l INNER JOIN fdw_push_right r ON l.k=r.k", []string{"count"}, []string{"INT8"}, [][]any{{int64(4)}}, false)
		fdwPushdownValue(run, "SELECT count(*) FROM fdw_push_left l CROSS JOIN fdw_push_right r", []string{"count"}, []string{"INT8"}, [][]any{{int64(20)}}, false)
		// Distinct occurrences of the same table must not collapse to one input.
		fdwPushdownValue(run, "SELECT a.k,a.v,b.v AS other_v FROM fdw_push_left a JOIN fdw_push_left b ON a.k=b.k", []string{"k", "v", "other_v"}, types, [][]any{{int64(1), int64(10), int64(10)}, {int64(1), int64(10), nil}, {int64(1), nil, int64(10)}, {int64(1), nil, nil}, {int64(2), int64(20), int64(20)}, {int64(4), int64(40), int64(40)}}, true)
		fdwPushdownValue(run, "SELECT count(*) FROM fdw_push_left a JOIN fdw_push_right b ON a.k=b.k JOIN fdw_push_left c ON b.k=c.k", []string{"count"}, []string{"INT8"}, [][]any{{int64(8)}}, false)
		// Parameter changes reuse one public prepared statement and real source.
		query := "SELECT l.k,l.v,r.w FROM fdw_push_left l JOIN fdw_push_right r ON l.k=r.k AND r.w=$1"
		fdwPushdownHeld(t, p, b, c, query,
			func() fixture.Step {
				s := fixture.Select(query, columns, types, inner, int64(100))
				s.Bag = true
				return s
			}(),
			fixture.Select(query, columns, types, nil, int64(300)))
		// A present all-NULL row is not an absent outer-join occurrence. Count
		// of the composite datum distinguishes them without exposing hidden slots.
		fdwSourceExec(t, p, b, r, run, `INSERT INTO fdw_source."Right input" VALUES(NULL,NULL)`, 1)
		fdwPushdownValue(run, "SELECT count(r) FROM fdw_push_left l LEFT JOIN fdw_push_right r ON l.k IS NULL AND r.k IS NULL AND r.w IS NULL", []string{"count"}, []string{"INT8"}, [][]any{{int64(1)}}, false)
	})
}
