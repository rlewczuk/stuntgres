package reference_test

import (
	"database/sql"
	"testing"

	"stuntgres/reference/fixture"
)

func TestReferencePolygonOperators(t *testing.T) {
	polygonScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
		run := func(s fixture.Step) { fixture.RunStep(t, p.Ctx, c, b.Recorder, s) }
		for _, tc := range []struct {
			left, right string
			want        bool
		}{
			{polygonTriangle, polygonTriangle, true}, {polygonTriangle, polygonRotated, true},
			{polygonTriangle, "((0,4),(4,0),(0,0))", true},
			{polygonTriangle, "((1,1),(5,1),(1,5))", false},
			{polygonTriangle, "((0,0),(4,0),(0,4),(0,0))", false},
			{"0,0,1,0,1,1,0,1", "0,0,1,1,1,0,0,1", false},
			{"0,0", "0.0000005,0", true}, {"0,0", "0.000002,0", false},
			{"NaN,0", "NaN,0", true}, {"NaN,0", "NaN,0.0000005", false},
			{"0,0,0,0,1,0,0,1", "0,0,1,0,0,1,0,0", true},
		} {
			q := "SELECT $1::polygon ~= $2::polygon AS same"
			s := fixture.Select(q, []string{"same"}, []string{"BOOL"}, [][]any{{tc.want}}, tc.left, tc.right)
			run(s)
			run(fixture.Prepared(s))
			run(fixture.Select("SELECT pg_catalog.poly_same("+boxQuote(tc.left)+"::polygon,"+boxQuote(tc.right)+"::polygon) AS same", []string{"same"}, []string{"BOOL"}, [][]any{{tc.want}}))
		}
		for _, triangle := range []string{polygonTriangle, "((0,4),(4,0),(0,0))"} {
			for _, tc := range []struct {
				point string
				want  bool
			}{{"(1,1)", true}, {"(2,2)", true}, {"(0,0)", true}, {"(3,3)", false}, {"(5,0)", false}} {
				for _, q := range []string{"SELECT $1::polygon @> $2::point AS inside", "SELECT $2::point <@ $1::polygon AS inside", "SELECT pg_catalog.poly_contain_pt($1::polygon,$2::point) AS inside", "SELECT pg_catalog.pt_contained_poly($2::point,$1::polygon) AS inside"} {
					s := fixture.Select(q, []string{"inside"}, []string{"BOOL"}, [][]any{{tc.want}}, triangle, tc.point)
					run(s)
					run(fixture.Prepared(s))
				}
			}
		}
		for _, signature := range []struct{ left, op, right, lv, rv string }{{"polygon", "~=", "polygon", polygonTriangle, polygonTriangle}, {"polygon", "@>", "point", polygonTriangle, "(1,1)"}, {"point", "<@", "polygon", "(1,1)", polygonTriangle}} {
			for _, args := range [][]any{{nil, signature.rv}, {signature.lv, nil}, {nil, nil}} {
				q := "SELECT $1::" + signature.left + " " + signature.op + " $2::" + signature.right + " AS v"
				s := fixture.Select(q, []string{"v"}, []string{"BOOL"}, [][]any{{nil}}, args...)
				run(s)
				run(fixture.Prepared(s))
				left, right := "NULL::"+signature.left, "NULL::"+signature.right
				if args[0] != nil {
					left = boxQuote(signature.lv) + "::" + signature.left
				}
				if args[1] != nil {
					right = boxQuote(signature.rv) + "::" + signature.right
				}
				run(fixture.Select("SELECT "+left+" "+signature.op+" "+right+" AS v", []string{"v"}, []string{"BOOL"}, [][]any{{nil}}))
			}
		}
		for _, tc := range []struct{ left, op, right string }{
			{"'0,0'::polygon", "~=", "'0,0'::polygon"},
			{"'0,0'::polygon", "@>", "'(0,0)'::point"},
			{"'(0,0)'::point", "<@", "'0,0'::polygon"},
		} {
			s := fixture.Select("SELECT "+tc.left+" OPERATOR(pg_catalog."+tc.op+") "+tc.right+" AS v", []string{"v"}, []string{"BOOL"}, [][]any{{true}})
			run(s)
			run(fixture.Prepared(s))
		}
		for _, op := range []string{"~=", "@>", "<@"} {
			q := "SELECT NULL::polygon " + op + " NULL::integer"
			for _, prepared := range []bool{false, true} {
				run(boxError(q, "42883", "operator does not exist: polygon "+op+" integer", fixture.Position(len("SELECT NULL::polygon ")+1), "No operator matches the given name and argument types. You might need to add explicit type casts.", prepared, false))
			}
		}
		t.Run("execution overflow discovery", func(t *testing.T) {
			run(fixture.Exec("CREATE TABLE polygon_overflow(v polygon,p point)", 0))
			run(fixture.Exec("INSERT INTO polygon_overflow VALUES('((1e308,1),(-1e308,-1),(0,2))','(-1e308,0)')", 1))
			for _, prepared := range []bool{false, true} {
				run(boxError("SELECT v @> p FROM polygon_overflow", "22003", "value out of range: overflow", "", "", prepared, true))
			}
		})
	})
}
