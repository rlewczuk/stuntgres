package reference_test

import (
	"fmt"
	"stuntgres/reference/fixture"
	"testing"
)

func TestReferenceQueryWindowOffsetDemand(t *testing.T) {
	queryWindowOffsetScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend) {
		queryWindowOffsetSeed(t, p, b)
		for _, fn := range []string{"lead", "lag"} {
			queryWindowOffsetVector(t, p, b, fn+"(1/(id-id),NULL::int,1/(id-id)) OVER(ORDER BY id)", []any{nil, nil, nil, nil, nil, nil}, "")
			queryWindowOffsetVector(t, p, b, fn+"(1/(id-id),100,id*100) OVER(ORDER BY id)", []any{int64(100), int64(200), int64(300), int64(400), int64(500), int64(600)}, "")
			queryWindowOffsetVector(t, p, b, fn+"(v,0,1/(id-id)) OVER(ORDER BY id)", []any{int64(10), nil, int64(30), int64(40), int64(50), nil}, "")
		}
		for _, mode := range []string{"ROWS", "RANGE", "GROUPS"} {
			for _, exclude := range []string{"NO OTHERS", "CURRENT ROW", "GROUP", "TIES"} {
				queryWindowOffsetVector(t, p, b, "lead(v) OVER w", []any{nil, int64(30), int64(40), nil, nil, nil}, "WINDOW w AS(PARTITION BY p ORDER BY id "+mode+" CURRENT ROW EXCLUDE "+exclude+")")
			}
		}
		queryWindowRoutes(t, p, b, fixture.Select("SELECT lead(id,0) OVER w AS x,count(*) OVER w AS c FROM wo WINDOW w AS(ORDER BY id ROWS CURRENT ROW EXCLUDE CURRENT ROW) ORDER BY id", []string{"x", "c"}, []string{"INT4", "INT8"}, [][]any{{int64(1), int64(0)}, {int64(2), int64(0)}, {int64(3), int64(0)}, {int64(4), int64(0)}, {int64(5), int64(0)}, {int64(6), int64(0)}}))
		for _, tc := range []struct {
			n             any
			code, message string
		}{{nil, "22004", "frame starting offset must not be null"}, {-1, "22013", "frame starting offset must not be negative"}} {
			// Frames do not select lead/lag members, but their bounds are still validated.
			queryWindowValueDiagnosticRoutes(t, p, b, "SELECT lead(7,0) OVER(ROWS $1 PRECEDING) AS x", fixture.Diagnostic{Code: tc.code, Message: tc.message}, "execute", tc.n)
			queryWindowValueDiagnosticRoutes(t, p, b, "SELECT lead(7,0) OVER w,count(*) OVER w WINDOW w AS(ROWS $1 PRECEDING)", fixture.Diagnostic{Code: tc.code, Message: tc.message}, "execute", tc.n)
		}
		queryWindowRun(t, p, b, fixture.Exec("CREATE SEQUENCE wo_seq START WITH 1", 0))
		for _, prepared := range []bool{false, true} {
			for _, tc := range []struct {
				name, expr, typ string
				values          []any
				called          bool
			}{
				{"value", "lead(nextval('wo_seq'),0,-1) OVER(ORDER BY id)", "INT8", []any{int64(1), int64(2), int64(3), int64(4), int64(5), int64(6)}, true},
				{"default", "lag(v,100,nextval('wo_seq')::int) OVER(ORDER BY id)", "INT4", []any{int64(1), int64(2), int64(3), int64(4), int64(5), int64(6)}, true},
				{"null-offset", "lead(nextval('wo_seq'),NULL::int,nextval('wo_seq')) OVER(ORDER BY id)", "INT8", []any{nil, nil, nil, nil, nil, nil}, false},
				{"offset", "lead(id,nextval('wo_seq')::int,-1) OVER(ORDER BY id)", "INT4", []any{int64(2), int64(4), int64(6), int64(-1), int64(-1), int64(-1)}, true},
				// Every row selects the same member, but the volatile value is not cached.
				{"repeated-member", "lead(nextval('wo_seq'),6-id,-1) OVER(ORDER BY id)", "INT8", []any{int64(1), int64(2), int64(3), int64(4), int64(5), int64(6)}, true},
			} {
				t.Run(fmt.Sprintf("%s/%t", tc.name, prepared), func(t *testing.T) {
					queryWindowRun(t, p, b, fixture.Exec("ALTER SEQUENCE wo_seq RESTART WITH 1", 0))
					var rows [][]any
					for _, v := range tc.values {
						rows = append(rows, []any{v})
					}
					s := fixture.Select("SELECT "+tc.expr+" AS x FROM wo ORDER BY id", []string{"x"}, []string{tc.typ}, rows)
					s.Prepared = prepared
					queryWindowRun(t, p, b, s)
					last := int64(1)
					if tc.called {
						last = 6
					}
					queryWindowRun(t, p, b, fixture.Select("SELECT last_value,is_called FROM wo_seq", []string{"last_value", "is_called"}, []string{"INT8", "BOOL"}, [][]any{{last, tc.called}}))
				})
			}
			for _, tc := range []struct {
				target string
				rows   [][]any
				last   int64
			}{
				{"lead(nextval('wo_seq'),0,-1) OVER w AS a,lead(nextval('wo_seq'),0,-1) OVER w AS b", [][]any{{int64(1), int64(1)}, {int64(2), int64(2)}, {int64(3), int64(3)}, {int64(4), int64(4)}, {int64(5), int64(5)}, {int64(6), int64(6)}}, 6},
				// A different third child must not match the retained whole sort target.
				{"lead(10,100,11) OVER w AS a,lead(10,100,22) OVER w AS b", [][]any{{int64(11), int64(22)}, {int64(11), int64(22)}, {int64(11), int64(22)}, {int64(11), int64(22)}, {int64(11), int64(22)}, {int64(11), int64(22)}}, 1},
			} {
				queryWindowRun(t, p, b, fixture.Exec("ALTER SEQUENCE wo_seq RESTART WITH 1", 0))
				typ := "INT4"
				if tc.last == 6 {
					typ = "INT8"
				}
				s := fixture.Select("SELECT "+tc.target+" FROM wo WINDOW w AS(ORDER BY id) ORDER BY a", []string{"a", "b"}, []string{typ, typ}, tc.rows)
				s.Prepared = prepared
				queryWindowRun(t, p, b, s)
				queryWindowRun(t, p, b, fixture.Select("SELECT last_value FROM wo_seq", []string{"last_value"}, []string{"INT8"}, [][]any{{tc.last}}))
			}
		}
	})
}
