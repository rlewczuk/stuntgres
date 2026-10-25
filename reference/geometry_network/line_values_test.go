package reference_test

import (
	"database/sql"
	"stuntgres/reference/fixture"
	"testing"
)

func lineValueCases(t *testing.T, cases [][2]string) {
	lineScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
		fixture.RunStep(t, p.Ctx, c, b.Recorder, fixture.Exec("SET extra_float_digits=3", 0))
		for _, tc := range cases {
			t.Run(tc[0], func(t *testing.T) {
				for _, prepared := range []bool{false, true} {
					for _, q := range []string{"SELECT line " + boxQuote(tc[0]) + " AS l", "SELECT pg_catalog.line " + boxQuote(tc[0]) + " AS l", "SELECT " + boxQuote(tc[0]) + "::line AS l", "SELECT CAST(" + boxQuote(tc[0]) + " AS line) AS l", "SELECT " + boxQuote(tc[0]) + "::text::line AS l"} {
						s := lineSelect(b, q, []string{"l"}, []string{"LINE"}, [][]any{{tc[1]}})
						s.Prepared = prepared
						fixture.RunStep(t, p.Ctx, c, b.Recorder, s)
					}
				}
			})
		}
	})
}

func TestReferenceLineValues(t *testing.T) {
	lineValueCases(t, [][2]string{
		{"{2,4,6}", "{2,4,6}"}, {"{1,2,3}", "{1,2,3}"},
		{"[(1,2),(3,4)]", "{1,-1,1}"}, {"((1,2),(3,4))", "{1,-1,1}"}, {"(1,2),(3,4)", "{1,-1,1}"}, {"1,2,3,4", "{1,-1,1}"},
		{"(3,4),(1,2)", "{1,-1,1}"}, {"(1,3),(2,3)", "{0,-1,3}"}, {"(3,1),(3,2)", "{-1,0,3}"},
		{"(0,0),(6,6)", "{1,-1,0}"}, {"10,-10,-5,-4", "{-0.4,-1,-6}"},
		{"{3,NaN,5}", "{3,NaN,5}"}, {"{NaN,NaN,NaN}", "{NaN,NaN,NaN}"}, {"{Infinity,1,-Infinity}", "{Infinity,1,-Infinity}"},
		{"{1,-0,-0}", "{1,-0,-0}"}, {"{1,2,5e-324}", "{1,2,5e-324}"}, {"{1,2,0.10000000000000002}", "{1,2,0.10000000000000002}"},
		{" { 2 , 4 , 6 } ", "{2,4,6}"}, {"{0.0000010001,0,3}", "{1.0001e-06,0,3}"},
	})
}

func TestReferenceLineScanner(t *testing.T) {
	lineValueCases(t, [][2]string{{"(1,2)(3,4)", "{1,-1,1}"}, {"1,2,3,4,", "{1,-1,1}"}, {"(1,2,3,4)", "{1,-1,1}"}, {"[(1,2),(3,4))", "{1,-1,1}"}})
}
