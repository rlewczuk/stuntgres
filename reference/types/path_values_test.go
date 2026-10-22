package reference_test

import (
	"database/sql"
	"testing"

	"stuntgres/reference/fixture"
)

func pathValueCases(t *testing.T, cases [][2]string) {
	pathScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
		run := func(s fixture.Step) { fixture.RunStep(t, p.Ctx, c, b.Recorder, s) }
		run(fixture.Exec("SET extra_float_digits=3", 0))
		run(fixture.Exec("CREATE TABLE path_values(id integer,p path)", 0))
		var want [][]any
		for i, tc := range cases {
			t.Run(tc[0], func(t *testing.T) {
				for _, prepared := range []bool{false, true} {
					for _, q := range []string{"SELECT path " + boxQuote(tc[0]) + " AS p", "SELECT pg_catalog.path " + boxQuote(tc[0]) + " AS p", "SELECT " + boxQuote(tc[0]) + "::path AS p", "SELECT CAST(" + boxQuote(tc[0]) + " AS path) AS p", "SELECT " + boxQuote(tc[0]) + "::text::path AS p", "SELECT " + boxQuote(tc[1]) + "::path::text::path AS p"} {
						s := pathSelect(b, q, []string{"p"}, []string{"PATH"}, [][]any{{tc[1]}})
						s.Prepared = prepared
						fixture.RunStep(t, p.Ctx, c, b.Recorder, s)
					}
				}
			})
			run(fixture.Exec("INSERT INTO path_values VALUES($1,$2)", 1, i+1, tc[0]))
			want = append(want, []any{int64(i + 1), tc[1]})
		}
		run(pathSelect(b, "SELECT id,p FROM path_values ORDER BY id", []string{"id", "p"}, []string{"INT4", "PATH"}, want))
	})
}

// Initial goldens are independently inferred from released path_in/path_encode,
// not learned from the native implementation. The pinned oracle settles edges.
func TestReferencePathValues(t *testing.T) {
	pathValueCases(t, [][2]string{
		{"[(1,2),(3,4)]", "[(1,2),(3,4)]"},
		{"((1,2),(3,4))", "((1,2),(3,4))"},
		{"(1,2),(3,4)", "((1,2),(3,4))"},
		{"(1,2,3,4)", "((1,2),(3,4))"},
		{"1,2,3,4", "((1,2),(3,4))"},
		{"[1,2,3,4]", "[(1,2),(3,4)]"},
		{"(1,2)", "((1,2))"}, {"1,2", "((1,2))"}, {"[1,2]", "[(1,2)]"},
		{"[(1,2),(1,2),(3,4),(1,2)]", "[(1,2),(1,2),(3,4),(1,2)]"},
		{"((9,8),(3,4),(1,2))", "((9,8),(3,4),(1,2))"},
		{" \t[ ( 1 , 2 ) , ( 3 , 4 ) ]\n", "[(1,2),(3,4)]"},
		{"[(-0,0.10000000000000002),(5e-324,-5e-324)]", "[(-0,0.10000000000000002),(5e-324,-5e-324)]"},
		{"[(NaN,Infinity),(-Infinity,1)]", "[(NaN,Infinity),(-Infinity,1)]"},
	})
}

func TestReferencePathScanner(t *testing.T) {
	pathValueCases(t, [][2]string{
		{"[(1,2),(3,4))", "[(1,2),(3,4)]"},
		{"((1,2))", "((1,2))"},
		{"[ (1,2) ]", "[(1,2)]"},
		{"( (1,2) , (3,4) )", "((1,2),(3,4))"},
	})
}
