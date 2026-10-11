package reference_test

import (
	"stuntgres/reference/fixture"
	"testing"
	"time"
)

func TestReferenceDateOrderingAndOperators(t *testing.T) {
	p := fixture.NewScenario(t)
	inputs := []string{"-infinity", "4714-11-24 BC", "0002-12-31 BC", "0001-01-01 BC", "0001-12-31 BC", "0001-01-01", "1900-02-28", "1900-03-01", "1999-12-31", "2000-01-01", "2000-02-28", "2000-02-29", "2000-03-01", "5874897-12-31", "infinity"}
	finite := []time.Time{time.Date(-4713, 11, 24, 0, 0, 0, 0, time.UTC), time.Date(-1, 12, 31, 0, 0, 0, 0, time.UTC), time.Date(0, 1, 1, 0, 0, 0, 0, time.UTC), time.Date(0, 12, 31, 0, 0, 0, 0, time.UTC), time.Date(1, 1, 1, 0, 0, 0, 0, time.UTC), time.Date(1900, 2, 28, 0, 0, 0, 0, time.UTC), time.Date(1900, 3, 1, 0, 0, 0, 0, time.UTC), time.Date(1999, 12, 31, 0, 0, 0, 0, time.UTC), time.Date(2000, 1, 1, 0, 0, 0, 0, time.UTC), time.Date(2000, 2, 28, 0, 0, 0, 0, time.UTC), time.Date(2000, 2, 29, 0, 0, 0, 0, time.UTC), time.Date(2000, 3, 1, 0, 0, 0, 0, time.UTC), time.Date(5874897, 12, 31, 0, 0, 0, 0, time.UTC)}
	for _, b := range p.Backends() {
		t.Run(b.Name, func(t *testing.T) {
			run := func(s fixture.Step) { fixture.DateRunStep(t, p.Ctx, b.DB, b.Recorder, s) }
			run(fixture.Exec("CREATE TABLE d(v DATE,w DATE)", 0))
			want := make([][]any, 0, len(inputs)+1)
			for i, input := range inputs {
				run(fixture.Exec("INSERT INTO d VALUES($1,$1)", 1, input))
				var v any = input
				if b.PG && i > 0 && i < len(inputs)-1 {
					v = finite[i-1]
				}
				want = append(want, []any{v})
			}
			run(fixture.Exec("INSERT INTO d VALUES(NULL,NULL)", 1))
			want = append(want, []any{nil})
			for _, prepared := range []bool{false, true} {
				s := fixture.Select("SELECT v FROM d ORDER BY v", []string{"v"}, []string{"DATE"}, want)
				s.Prepared = prepared
				run(s)
				rev := make([][]any, len(want))
				for i := range want {
					rev[i] = want[len(want)-1-i]
				}
				s = fixture.Select("SELECT v FROM d ORDER BY v DESC", []string{"v"}, []string{"DATE"}, rev)
				s.Prepared = prepared
				run(s)
				for _, tc := range []struct {
					op   string
					want bool
				}{{"=", false}, {"<>", true}, {"!=", true}, {"<", true}, {"<=", true}, {">", false}, {">=", false}} {
					s = fixture.Select("SELECT v"+tc.op+"DATE '2000-03-01',DATE '2000-02-28'"+tc.op+"v,v=w FROM d WHERE v=$1", []string{"?column?", "?column?", "?column?"}, []string{"BOOL", "BOOL", "BOOL"}, [][]any{{tc.want, tc.want, true}}, "2000-02-29")
					s.Prepared = prepared
					run(s)
					s = fixture.Select("SELECT v FROM d WHERE $1"+tc.op+"v ORDER BY v", []string{"v"}, []string{"DATE"}, nil, nil)
					s.Prepared = prepared
					run(s)
					s = fixture.Select("SELECT $1"+tc.op+"v FROM d WHERE v='2000-02-29'", []string{"?column?"}, []string{"BOOL"}, [][]any{{tc.want}}, "2000-02-28")
					s.Prepared = prepared
					run(s)
					indices := map[string][]int{"=": {11}, "<>": {0, 1, 2, 3, 4, 5, 6, 7, 8, 9, 10, 12, 13, 14}, "!=": {0, 1, 2, 3, 4, 5, 6, 7, 8, 9, 10, 12, 13, 14}, "<": {0, 1, 2, 3, 4, 5, 6, 7, 8, 9, 10}, "<=": {0, 1, 2, 3, 4, 5, 6, 7, 8, 9, 10, 11}, ">": {12, 13, 14}, ">=": {11, 12, 13, 14}}
					for _, leftParameter := range []bool{false, true} {
						op := tc.op
						predicate := "v" + op + "$1"
						if leftParameter {
							predicate = "$1" + op + "v"
							op = map[string]string{"=": "=", "<>": "<>", "!=": "!=", "<": ">", "<=": ">=", ">": "<", ">=": "<="}[op]
						}
						selected := make([][]any, 0, len(indices[op]))
						for _, i := range indices[op] {
							selected = append(selected, want[i])
						}
						s = fixture.Select("SELECT v FROM d WHERE "+predicate+" ORDER BY v", []string{"v"}, []string{"DATE"}, selected, "2000-02-29")
						s.Prepared = prepared
						run(s)
					}
				}
			}
		})
	}
}

func TestReferenceDateGregorianOrdering(t *testing.T) {
	p := fixture.NewScenario(t)
	inputs := []any{"-infinity", "4714-11-24 BC", "0002-12-31 BC", "0001-01-01 BC", "0001-12-31 BC", "0001-01-01", "1900-02-28", "1900-03-01", "1999-12-31", "2000-01-01", "2000-02-28", "2000-02-29", "2000-03-01", "5874897-12-31", "infinity", nil}
	for _, b := range p.Backends() {
		t.Run(b.Name, func(t *testing.T) {
			run := func(s fixture.Step) { fixture.DateRunStep(t, p.Ctx, b.DB, b.Recorder, s) }
			run(fixture.Exec("CREATE TABLE d(v DATE)", 0))
			for i := len(inputs) - 1; i >= 0; i-- {
				run(fixture.Exec("INSERT INTO d VALUES($1)", 1, inputs[i]))
			}
			want, reverse := make([][]any, len(inputs)), make([][]any, len(inputs))
			for i, input := range inputs {
				var value any
				if input != nil {
					value = fixture.DateExpected(b.PG, input.(string))
				}
				want[i] = []any{value}
				reverse[len(inputs)-1-i] = want[i]
			}
			run(fixture.Select("SELECT v FROM d ORDER BY v", []string{"v"}, []string{"DATE"}, want))
			run(fixture.Select("SELECT v FROM d ORDER BY v DESC", []string{"v"}, []string{"DATE"}, reverse))
		})
	}
}
