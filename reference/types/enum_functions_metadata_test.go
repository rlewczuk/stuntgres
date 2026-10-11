package reference_test

import (
	"database/sql"
	"stuntgres/reference/fixture"
	"testing"
)

func TestReferenceEnumFunctionsMetadata(t *testing.T) {
	enumFunctionsScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, conn *sql.Conn) {
		for _, tc := range []struct {
			query string
			rows  [][]any
			array bool
			args  []any
		}{
			{"SELECT enum_first(NULL::mood) AS v", [][]any{{"z"}}, false, nil},
			{"SELECT enum_last($1::mood) AS v", [][]any{{"m"}}, false, []any{nil}},
			{"SELECT enum_range($1::mood,$2::mood) AS v", [][]any{{"{z,a}"}}, true, []any{nil, "a"}},
			{"SELECT enum_first(lo) AS v FROM enum_endpoints ORDER BY id", [][]any{{"z"}, {"z"}, {"z"}, {"z"}, {"z"}}, false, nil},
			{"SELECT enum_last(lo) AS v FROM enum_endpoints WHERE id=99", nil, false, nil},
			{"SELECT enum_first(lo) AS v FROM enum_endpoints WHERE id=5", [][]any{{"z"}}, false, nil},
			{"SELECT enum_range(lo,hi) AS v FROM enum_endpoints ORDER BY id", [][]any{{"{z,a}"}, {"{a,m}"}, {"{z,a}"}, {"{a,m}"}, {"{z,a,m}"}}, true, nil},
			{"SELECT enum_range(lo) AS v FROM enum_endpoints WHERE id=99", nil, true, nil},
			{"SELECT enum_range(lo,hi) AS v FROM enum_endpoints WHERE id=5", [][]any{{"{z,a,m}"}}, true, nil},
		} {
			t.Run(tc.query, func(t *testing.T) {
				for _, prepared := range []bool{false, true} {
					name := ""
					if tc.array {
						name = enumFunctionsTypeName(b.PG, "_MOOD")
					}
					s := fixture.Select(tc.query, []string{"v"}, []string{name}, tc.rows, tc.args...)
					s.RawTypes = []string{"_ENUM"}
					s.Prepared = prepared
					s.CheckMetadata = enumFunctionsMetadata(b.PG, tc.array)
					fixture.EnumReferenceRunStep(t, p.Ctx, conn, b.Recorder, s)
				}
			})
		}
	})
}
