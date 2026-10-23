package reference_test

import (
	"database/sql"
	"testing"

	"stuntgres/reference/fixture"
)

func TestReferenceGeometryMeasureBoxLineValues(t *testing.T) {
	geometryMeasureScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
		check := func(t *testing.T, q string, names, types []string, wants [][]any) {
			t.Helper()
			s := geometryMeasureSelect(b, q, names, types, wants)
			fixture.RunStep(t, p.Ctx, c, b.Recorder, s)
			fixture.RunStep(t, p.Ctx, c, b.Recorder, fixture.Prepared(s))
		}
		for _, tc := range []struct {
			input, diagonal string
			width, height   float64
		}{
			{"(4,6),(1,2)", "[(4,6),(1,2)]", 3, 4},
			{"(1,2),(4,6)", "[(4,6),(1,2)]", 3, 4},
			{"(1,6),(4,2)", "[(4,6),(1,2)]", 3, 4},
			{"(1,2),(1,2)", "[(1,2),(1,2)]", 0, 0},
		} {
			t.Run(tc.input, func(t *testing.T) {
				for _, prefix := range []string{"", "pg_catalog."} {
					check(t, "SELECT "+prefix+"width(box '"+tc.input+"') AS w,"+prefix+"height(box '"+tc.input+"') AS h,"+prefix+"diagonal(box '"+tc.input+"') AS d", []string{"w", "h", "d"}, []string{"FLOAT8", "FLOAT8", "LSEG"}, [][]any{{tc.width, tc.height, tc.diagonal}})
				}
			})
		}
		fixture.RunStep(t, p.Ctx, c, b.Recorder, fixture.Exec("CREATE TABLE geom_box_line_raw(b box)", 0))
		fixture.RunStep(t, p.Ctx, c, b.Recorder, fixture.Exec("INSERT INTO geom_box_line_raw VALUES(box '(4,6),(1,2)')", 1))
		fixture.RunStep(t, p.Ctx, c, b.Recorder, fixture.Exec("UPDATE geom_box_line_raw SET b[0]=point '(0,0)'", 1))
		check(t, "SELECT width(b) AS w,height(b) AS h,diagonal(b) AS d FROM geom_box_line_raw", []string{"w", "h", "d"}, []string{"FLOAT8", "FLOAT8", "LSEG"}, [][]any{{float64(-1), float64(-2), "[(0,0),(1,2)]"}})
		// LINE fixtures retain their defining points; only LSEG exposes endpoints.
		for _, tc := range []struct{ a, z, slope string }{
			{"(0,0)", "(2,1)", "0.5"}, {"(0,1)", "(2,0)", "-0.5"},
			{"(1,0)", "(1,2)", "Infinity"}, {"(0,2)", "(3,2)", "0"},
		} {
			t.Run(tc.a+tc.z, func(t *testing.T) {
				q := "SELECT line(p,q) IS NOT NULL AS line_ok,slope(p,q)::text AS forward,slope(q,p)::text AS reverse,slope(s[0],s[1])::text AS segment FROM (SELECT point '" + tc.a + "' AS p,point '" + tc.z + "' AS q,lseg(point '" + tc.a + "',point '" + tc.z + "') AS s) AS endpoints"
				check(t, q, []string{"line_ok", "forward", "reverse", "segment"}, []string{"BOOL", "TEXT", "TEXT", "TEXT"}, [][]any{{true, tc.slope, tc.slope, tc.slope}})
			})
		}
		for _, tc := range []struct{ expression, want string }{
			{"slope(point '(1,2)',point '(1,2)')", "Infinity"},
			{"slope(point '(0,0)',point '(0.0000005,2)')", "Infinity"},
			{"slope(point '(0,0)',point '(2,0.0000005)')", "0"},
			{"slope(point '(0,0)',point '(0.0000005,0)')", "Infinity"},
			{"slope(point '(NaN,0)',point '(1,2)')", "NaN"},
			{"slope(point '(Infinity,0)',point '(Infinity,2)')", "Infinity"},
			{"slope(point '(-0,-0)',point '(2,0)')", "0"},
			{"width(box '(Infinity,2),(0,0)')", "Infinity"},
		} {
			t.Run(tc.expression, func(t *testing.T) {
				check(t, "SELECT "+tc.expression+"::text AS v", []string{"v"}, []string{"TEXT"}, [][]any{{tc.want}})
			})
		}
		for _, expression := range []string{"width(NULL)", "height(NULL)", "diagonal(NULL)", "width(NULL::box)", "height(NULL::box)", "diagonal(NULL::box)", "slope(NULL,point '(2,1)')", "slope(point '(0,0)',NULL)", "slope(NULL,NULL)", "slope(NULL::point,NULL::point)"} {
			typ := "FLOAT8"
			if expression == "diagonal(NULL)" || expression == "diagonal(NULL::box)" {
				typ = "LSEG"
			}
			check(t, "SELECT "+expression+" AS v", []string{"v"}, []string{typ}, [][]any{{nil}})
		}
	})
}
