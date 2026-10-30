package reference_test

import (
	"database/sql"
	"fmt"
	"testing"

	"stuntgres/reference/fixture"
)

func TestReferenceCubeConstructors(t *testing.T) {
	// PostgreSQL manual F.10 and REL_18_6 cube.c specify these rows before
	// oracle execution. In particular, constructors do not sort endpoints.
	cases := []struct {
		name, expression, text string
		dimension              int64
		point                  bool
	}{
		{"scalar", "cube(1)", "(1)", 1, true},
		{"scalar_box", "cube(1,2)", "(1),(2)", 1, false},
		{"reversed", "cube(2,1)", "(2),(1)", 1, false},
		{"equal", "cube(1,1)", "(1)", 1, true},
		{"array", "cube(ARRAY[1,2,3])", "(1, 2, 3)", 3, true},
		{"required_box", "cube(ARRAY[1,2],ARRAY[3,4])", "(1, 2),(3, 4)", 2, false},
		{"array_equal", "cube(ARRAY[1,2],ARRAY[1,2])", "(1, 2)", 2, true},
		{"array_reversed", "cube(ARRAY[3,4],ARRAY[1,2])", "(3, 4),(1, 2)", 2, false},
		{"extend_point", "cube(cube(1),2)", "(1, 2)", 2, true},
		{"extend_box", "cube('(1,2),(3,4)'::cube,5)", "(1, 2, 5),(3, 4, 5)", 3, false},
		{"extend_two", "cube('(1,2),(3,4)'::cube,5,6)", "(1, 2, 5),(3, 4, 6)", 3, false},
		{"extend_point_equal", "cube(cube(1),2,2)", "(1, 2)", 2, true},
		{"extend_point_box", "cube(cube(1),3,2)", "(1, 3),(1, 2)", 2, false},
		{"empty", "cube('{}'::double precision[])", "()", 0, true},
		{"empty_pair", "cube('{}'::double precision[],'{}'::double precision[])", "()", 0, true},
		{"empty_extend", "cube(cube('{}'::double precision[]),5)", "(5)", 1, true},
		{"empty_extend_two", "cube('()'::cube,5,6)", "(5),(6)", 1, false},
		{"rank_two", "cube(ARRAY[[1,2],[3,4]])", "(1, 2, 3, 4)", 4, true},
		{"lower_bounds", "cube('[-2:-1][3:4]={{1,2},{3,4}}'::double precision[])", "(1, 2, 3, 4)", 4, true},
		{"different_shapes", "cube('{{1,2},{3,4}}'::double precision[],'[0:3]={5,6,7,8}'::double precision[])", "(1, 2, 3, 4),(5, 6, 7, 8)", 4, false},
		{"negative_zero", "cube('-0'::double precision,0::double precision)", "(-0)", 1, true},
		{"positive_zero", "cube(0::double precision,'-0'::double precision)", "(0)", 1, true},
		{"nan_point", "cube('NaN'::double precision)", "(NaN)", 1, true},
		{"nan_array_point", "cube(ARRAY['NaN'::double precision])", "(NaN)", 1, true},
		{"nan_box", "cube('NaN'::double precision,'NaN'::double precision)", "(NaN),(NaN)", 1, false},
		{"nan_arrays", "cube(ARRAY['NaN'::double precision],ARRAY['NaN'::double precision])", "(NaN),(NaN)", 1, false},
		{"nan_extension", "cube(cube('NaN'::double precision),'NaN'::double precision)", "(NaN, NaN)", 2, true},
		{"nan_extension_two", "cube(cube(1),'NaN'::double precision,'NaN'::double precision)", "(1, NaN),(1, NaN)", 2, false},
		{"infinity_equal", "cube('Infinity'::double precision,'Infinity'::double precision)", "(Infinity)", 1, true},
		{"infinities", "cube(ARRAY['Infinity'::double precision,'-Infinity'::double precision])", "(Infinity, -Infinity)", 2, true},
		{"adjacent_binary64", "cube(0.10000000000000002::double precision)", "(0.10000000000000002)", 1, true},
		{"subnormal", "cube('5e-324'::double precision)", "(5e-324)", 1, true},
		{"dimension_100", "cube(" + cubeLiteral(cubeConstructorArray(100, "1")) + "::double precision[])", cubeConstructorPoint(100, "1"), 100, true},
		{"extend_99", "cube(cube(" + cubeLiteral(cubeConstructorArray(99, "1")) + "::double precision[]),1)", cubeConstructorPoint(100, "1"), 100, true},
		{"extend_99_two", "cube(cube(" + cubeLiteral(cubeConstructorArray(99, "1")) + "::double precision[]),1,1)", cubeConstructorPoint(100, "1"), 100, true},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			cubeConstructorScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
				cubeInstall(t, p, b, c)
				q := fmt.Sprintf("SELECT %s AS v,cube_dim(%s) AS dim,cube_is_point(%s) AS point", tc.expression, tc.expression, tc.expression)
				s := cubeConstructorSelect(b, q, []string{"v", "dim", "point"}, []string{"CUBE", "INT4", "BOOL"}, [][]any{{tc.text, tc.dimension, tc.point}})
				fixture.RunStep(t, p.Ctx, c, b.Recorder, s)
				fixture.RunStep(t, p.Ctx, c, b.Recorder, fixture.Prepared(s))
			})
		})
	}
	t.Run("strict_each_formal", func(t *testing.T) {
		cubeConstructorScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
			cubeInstall(t, p, b, c)
			for _, expr := range []string{
				"cube(NULL::double precision)", "cube(NULL::double precision,1)", "cube(1,NULL::double precision)",
				"cube(NULL::double precision[])", "cube(NULL::double precision[],ARRAY[1])", "cube(ARRAY[1],NULL::double precision[])",
				"cube(NULL::cube,1)", "cube(cube(1),NULL::double precision)",
				"cube(NULL::cube,1,2)", "cube(cube(1),NULL::double precision,2)", "cube(cube(1),2,NULL::double precision)",
				"cube(ARRAY[NULL::double precision],NULL::double precision[])",
				"cube(cube(" + cubeLiteral(cubeConstructorArray(100, "1")) + "::double precision[]),NULL::double precision)",
				"cube(" + cubeLiteral(cubeConstructorArray(101, "1")) + "::double precision[],NULL::double precision[])",
				"cube(NULL::double precision[]," + cubeLiteral(cubeConstructorArray(101, "1")) + "::double precision[])",
				"cube(cube(" + cubeLiteral(cubeConstructorArray(100, "1")) + "::double precision[]),NULL::double precision,2)",
				"cube(cube(" + cubeLiteral(cubeConstructorArray(100, "1")) + "::double precision[]),2,NULL::double precision)",
			} {
				s := cubeConstructorSelect(b, "SELECT "+expr+" AS v", []string{"v"}, []string{"CUBE"}, [][]any{{nil}})
				fixture.RunStep(t, p.Ctx, c, b.Recorder, s)
				fixture.RunStep(t, p.Ctx, c, b.Recorder, fixture.Prepared(s))
			}
			for _, tc := range []struct{ expr, typ string }{{"cube_dim(NULL::cube)", "INT4"}, {"cube_is_point(NULL::cube)", "BOOL"}} {
				s := fixture.Select("SELECT "+tc.expr+" AS v", []string{"v"}, []string{tc.typ}, [][]any{{nil}})
				fixture.RunStep(t, p.Ctx, c, b.Recorder, s)
				fixture.RunStep(t, p.Ctx, c, b.Recorder, fixture.Prepared(s))
			}
		})
	})
}
