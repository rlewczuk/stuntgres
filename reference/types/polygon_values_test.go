package reference_test

import (
	"database/sql"
	"testing"

	"stuntgres/reference/fixture"
)

func TestReferencePolygonValues(t *testing.T) {
	polygonScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
		run := func(s fixture.Step) { fixture.RunStep(t, p.Ctx, c, b.Recorder, s) }
		run(fixture.Exec("CREATE TABLE polygon_values(id integer NOT NULL,v polygon)", 0))
		for i, input := range []string{polygonTriangle, "(0,0),(4,0),(0,4)", "(0,0,4,0,0,4)", "0,0,4,0,0,4"} {
			run(fixture.Exec("INSERT INTO polygon_values VALUES($1,$2)", 1, i+1, input))
			for _, expression := range []string{"polygon " + boxQuote(input), "pg_catalog.polygon " + boxQuote(input), "CAST(" + boxQuote(input) + " AS polygon)", boxQuote(input) + "::polygon"} {
				s := polygonSelect(b, "SELECT "+expression+" AS v", []string{"v"}, []string{"POLYGON"}, [][]any{{polygonTriangle}})
				run(s)
				run(fixture.Prepared(s))
			}
		}
		run(polygonSelect(b, "SELECT id,v FROM polygon_values ORDER BY id", []string{"id", "v"}, []string{"INT4", "POLYGON"}, [][]any{{int64(1), polygonTriangle}, {int64(2), polygonTriangle}, {int64(3), polygonTriangle}, {int64(4), polygonTriangle}}))
		for _, tc := range []struct{ input, output string }{
			{"0,0", "((0,0))"}, {"0,0,1,1", "((0,0),(1,1))"},
			{"0,0,4,0,0,4,0,0", "((0,0),(4,0),(0,4),(0,0))"},
			{polygonRotated, polygonRotated}, {"((0,4),(4,0),(0,0))", "((0,4),(4,0),(0,0))"},
			{"5e-324,-0", "((5e-324,-0))"}, {"NaN,Infinity", "((NaN,Infinity))"},
		} {
			s := polygonSelect(b, "SELECT $1::polygon AS v", []string{"v"}, []string{"POLYGON"}, [][]any{{tc.output}}, tc.input)
			run(s)
			run(fixture.Prepared(s))
		}
		run(fixture.Exec("INSERT INTO polygon_values VALUES(5,'0,0'),(6,NULL)", 2))
		run(fixture.Prepared(fixture.Exec("INSERT INTO polygon_values VALUES(7,$1)", 1, []byte("0,0,1,1"))))
		run(polygonSelect(b, "SELECT id,v FROM polygon_values WHERE id>=5 ORDER BY id", []string{"id", "v"}, []string{"INT4", "POLYGON"}, [][]any{{int64(5), "((0,0))"}, {int64(6), nil}, {int64(7), "((0,0),(1,1))"}}))
	})
}
