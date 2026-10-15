package reference_test

import (
	"testing"

	"stuntgres/reference/fixture"
)

func TestReferenceQueryCycleMarks(t *testing.T) {
	p := fixture.NewScenario(t)
	for _, backend := range p.Backends() {
		t.Run(backend.Name, func(t *testing.T) {
			c := cycleConn(t, p, backend)
			for _, tc := range []struct {
				name, clause, typ string
				marks             []any
			}{
				{"default boolean", "", "BOOL", []any{false, true}},
				{"text", " TO 'Y' DEFAULT 'N'", "TEXT", []any{"N", "Y"}},
				{"integer", " TO 1 DEFAULT 0", "INT4", []any{int64(0), int64(1)}},
				{"equal integers", " TO 1 DEFAULT 1", "INT4", []any{int64(1)}},
				{"null cycle", " TO NULL DEFAULT 0", "INT4", []any{int64(0)}},
				{"null default", " TO 1 DEFAULT NULL", "INT4", []any{nil}},
				{"both null", " TO NULL DEFAULT NULL", "TEXT", []any{nil}},
			} {
				t.Run(tc.name, func(t *testing.T) {
					q := "WITH RECURSIVE r(n) AS (SELECT 1 UNION ALL SELECT n FROM r) CYCLE n SET cyc" + tc.clause + " USING path SELECT n,cyc,path FROM r"
					rows := [][]any{{int64(1), tc.marks[0], "{(1)}"}}
					if len(tc.marks) == 2 {
						rows = append(rows, []any{int64(1), tc.marks[1], "{(1),(1)}"})
					}
					subqueryBoth(subqueryRun(t, p, backend, c), cteBag(q, []string{"n", "cyc", "path"}, []string{"INT4", tc.typ, "_RECORD"}, rows))
				})
			}
			for _, tc := range []struct {
				name, clause, typ        string
				marks                    []any
				length, precision, scale int64
			}{
				{"mixed integer widths", " TO bigint '1' DEFAULT integer '0'", "INT8", []any{int64(0), int64(1)}, 0, 0, 0},
				{"mixed numeric", " TO numeric '1.50' DEFAULT integer '0'", "NUMERIC", []any{"0", "1.50"}, 0, 0, 0},
				{"numeric typmod", " TO numeric(4,2) '1.50' DEFAULT numeric(4,2) '0.00'", "NUMERIC", []any{"0.00", "1.50"}, 0, 4, 2},
				{"character typmod", " TO char(2) 'Y' DEFAULT char(2) 'N'", "BPCHAR", []any{"N ", "Y "}, 2, 0, 0},
				{"varchar typmod", " TO varchar(3) 'yes' DEFAULT varchar(3) 'no'", "VARCHAR", []any{"no", "yes"}, 3, 0, 0},
			} {
				t.Run(tc.name, func(t *testing.T) {
					body := "WITH RECURSIVE r(n) AS (SELECT 1 UNION ALL SELECT n FROM r) CYCLE n SET cyc" + tc.clause + " USING path "
					t.Run("common usage all routes", func(t *testing.T) {
						s := cteBag(body+"SELECT n,cyc,path FROM r", []string{"n", "cyc", "path"}, []string{"INT4", tc.typ, "_RECORD"}, [][]any{{int64(1), tc.marks[0], "{(1)}"}, {int64(1), tc.marks[1], "{(1),(1)}"}})
						s.CheckMetadata = cycleMarkMetadata(backend, s.Columns, s.Types, tc.length, tc.precision, tc.scale)
						recursiveCTERoutes(t, p, backend, c, s)
					})
					t.Run("independent marks", func(t *testing.T) {
						subqueryBoth(subqueryRun(t, p, backend, c), cteBag(body+"SELECT n,cyc,path FROM r", []string{"n", "cyc", "path"}, []string{"INT4", tc.typ, "_RECORD"}, [][]any{{int64(1), tc.marks[0], "{(1)}"}, {int64(1), tc.marks[1], "{(1),(1)}"}}))
					})
				})
			}
		})
	}
}
