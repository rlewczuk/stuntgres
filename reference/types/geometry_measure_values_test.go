package reference_test

import (
	"database/sql"
	"testing"

	"stuntgres/reference/fixture"
)

func TestReferenceGeometryMeasureValues(t *testing.T) {
	geometryMeasureScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
		check := func(t *testing.T, expression, label, typ string, want any) {
			t.Helper()
			s := geometryMeasureSelect(b, "SELECT "+expression, []string{label}, []string{typ}, [][]any{{want}})
			fixture.RunStep(t, p.Ctx, c, b.Recorder, s)
			fixture.RunStep(t, p.Ctx, c, b.Recorder, fixture.Prepared(s))
		}
		for _, tc := range geometryMeasureCases {
			t.Run(tc.name, func(t *testing.T) {
				expression := tc.function + "(" + tc.formal + " '" + tc.input + "')"
				check(t, expression, tc.function, tc.typ, tc.want)
				check(t, "pg_catalog."+expression+" AS v", "v", tc.typ, tc.want)
				check(t, tc.function+"(NULL::"+tc.formal+") AS v", "v", tc.typ, nil)
			})
		}
		for _, tc := range []struct {
			expression, typ string
			want            any
		}{
			{"area(path '[(0,0),(4,0),(0,3)]')", "FLOAT8", nil},
			{"area(path '[(1e308,1e308)]')", "FLOAT8", nil},
			{"area(path '((0,3),(4,0),(0,0))')", "FLOAT8", float64(6)},
			{"area(path '((0,0),(4,0),(0,3),(0,0))')", "FLOAT8", float64(6)},
			{"area(path(polygon '((0,0),(4,0),(0,3))'))", "FLOAT8", float64(6)},
			{"area(path '((2,3))')", "FLOAT8", float64(0)},
			{"area(path '((2,3),(4,5))')", "FLOAT8", float64(0)},
			{"area(path '((0,0),(2,2),(0,2),(2,0))')", "FLOAT8", float64(0)},
			{"area(box '(0,0),(0,0)')", "FLOAT8", float64(0)},
			{"center(box '(0,0),(0,0)')", "POINT", "(0,0)"},
			{"area(circle '<(0,0),0>')", "FLOAT8", float64(0)},
			{"center(circle '<(0,0),0>')", "POINT", "(0,0)"},
			{"diameter(circle '<(0,0),0>')", "FLOAT8", float64(0)},
			{"radius(circle '<(0,0),0>')", "FLOAT8", float64(0)},
			{"area(circle(point '(0,0)',-2))", "FLOAT8", float64(12.566370614359172)},
			{"diameter(circle(point '(0,0)',-2))", "FLOAT8", float64(-4)},
			{"radius(circle(point '(0,0)',-2))", "FLOAT8", float64(-2)},
			{"center(circle(point '(0,0)',-2))", "POINT", "(0,0)"},
			{"center(circle '<(-0,0),NaN>')", "POINT", "(-0,0)"},
			{"center(circle '<(NaN,Infinity),2>')", "POINT", "(NaN,Infinity)"},
			{"center(circle '<(0.10000000000000002,5e-324),2>')", "POINT", "(0.10000000000000002,5e-324)"},
			{"center(pg_catalog.circle '<(3,-4),2>')", "POINT", "(3,-4)"},
			{"area(\"circle\" '<(0,0),2>')", "FLOAT8", float64(12.566370614359172)},
			{"area(circle '<(0,0),Infinity>') = 'Infinity'::float8", "BOOL", true},
			{"area(circle '<(0,0),NaN>') = 'NaN'::float8", "BOOL", true},
			{"diameter(circle(point '(0,0)','-Infinity'::float8)) = '-Infinity'::float8", "BOOL", true},
			{"diameter(NULL)", "FLOAT8", nil},
		} {
			t.Run(tc.expression, func(t *testing.T) { check(t, tc.expression+" AS v", "v", tc.typ, tc.want) })
		}
	})
}
