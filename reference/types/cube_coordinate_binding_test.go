package reference_test

import (
	"database/sql"
	"strings"
	"testing"

	"stuntgres/reference/fixture"
)

func TestReferenceCubeCoordinateBinding(t *testing.T) {
	cubeScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
		cubeCoordinateSeed(t, p, b, c)
		run := func(s fixture.Step) { fixture.RunStep(t, p.Ctx, c, b.Recorder, s) }
		cubeSpatialBoth(t, p, b, c, cubeCoordinateTable(b, false))
		q := "SELECT " + cubeCoordinateProjection("$1::cube", "$2::integer")
		held := cubeCoordinateHeld(t, p, c, q)
		owned := []byte("(3,1),(1,4)")
		var absent []byte
		for _, tc := range []struct {
			c, n any
			want []any
		}{
			{"(3,1),(1,4)", 1, []any{float64(1), float64(3), float64(3), float64(1)}},
			{owned, 2, []any{float64(1), float64(4), float64(1), float64(3)}},
			{"(42,137)", 2, []any{float64(137), float64(137), float64(137), float64(42)}},
			{nil, 0, []any{nil, nil, nil, nil}}, {absent, 0, []any{nil, nil, nil, nil}},
			{"(1)", nil, []any{nil, nil, nil, nil}},
		} {
			s := cubeCoordinateSelect(b, q, [][]any{tc.want}, tc.c, tc.n)
			cubeSpatialBoth(t, p, b, c, s)
			fixture.RunStep(t, p.Ctx, held, b.Recorder, s)
		}
		for i := range owned {
			owned[i] = 'x'
		}
		cubeSpatialBoth(t, p, b, c, cubeCoordinateTable(b, false))
		for _, pred := range []string{"id=3", "id=99"} {
			var want [][]any
			if pred == "id=3" {
				want = [][]any{{nil, nil, nil, nil}}
			} else {
				want = [][]any{}
			}
			cubeSpatialBoth(t, p, b, c, cubeCoordinateSelect(b, "SELECT "+cubeCoordinateProjection("v", "1")+" FROM cube_coordinate_data WHERE "+pred, want))
		}
		cubeSpatialBoth(t, p, b, c, cubeCoordinateSelect(b, "SELECT "+cubeCoordinateProjection("'(3,1),(1,4)'::cube", "1::smallint"), [][]any{{float64(1), float64(3), float64(3), float64(1)}}))
		cubeSpatialBoth(t, p, b, c, fixture.Select("SELECT '[10,20]'::json -> 1 AS j,'[30,40]'::jsonb -> 0 AS b", []string{"j", "b"}, []string{"JSON", "JSONB"}, [][]any{{"20", "30"}}))
		for _, tc := range []struct {
			q, message, token string
			operator          bool
		}{
			{"SELECT cube_ll_coord('(1)'::cube,1::bigint)", "function cube_ll_coord(cube, bigint) does not exist", "cube_ll_coord", false},
			{"SELECT cube_ur_coord('(1)'::cube,'1'::text)", "function cube_ur_coord(cube, text) does not exist", "cube_ur_coord", false},
			{"SELECT cube_ll_coord('(1)'::text,1)", "function cube_ll_coord(text, integer) does not exist", "cube_ll_coord", false},
			{"SELECT cube_ll_coord('(1)'::cube)", "function cube_ll_coord(cube) does not exist", "cube_ll_coord", false},
			{"SELECT '(1)'::cube -> 1::bigint", "operator does not exist: cube -> bigint", "->", true},
			{"SELECT '(1)'::cube ~> '1'::text", "operator does not exist: cube ~> text", "~>", true},
		} {
			want := fixture.ControlsReferenceDiagnostic("42883", tc.message)
			want.Hint = "No function matches the given name and argument types. You might need to add explicit type casts."
			if tc.operator {
				want.Hint = "No operator matches the given name and argument types. You might need to add explicit type casts."
			}
			want.Position = cubeSpatialPosition(tc.q[:strings.Index(tc.q, tc.token)])
			for _, prepared := range []bool{false, true} {
				cubeError(t, p, b, c, tc.q, prepared, true, true, want)
			}
		}
		run(fixture.Exec("SET search_path=pg_catalog", 0))
		qualified := "SELECT public.cube_ll_coord('(3,1),(1,4)'::public.cube,1) AS lower,public.cube_ur_coord('(3,1),(1,4)'::public.cube,1) AS upper,'(3,1),(1,4)'::public.cube OPERATOR(public.->) 1 AS raw,'(3,1),(1,4)'::public.cube OPERATOR(public.~>) 1 AS projection"
		cubeSpatialBoth(t, p, b, c, cubeCoordinateSelect(b, qualified, [][]any{{float64(1), float64(3), float64(3), float64(1)}}))
		for _, tc := range []struct{ q, message, token, hint string }{
			{"SELECT cube_ll_coord('(1)'::public.cube,1)", "function cube_ll_coord(public.cube, integer) does not exist", "cube_ll_coord", "No function matches the given name and argument types. You might need to add explicit type casts."},
			{"SELECT '(1)'::public.cube ~> 1", "operator does not exist: public.cube ~> integer", "~>", "No operator matches the given name and argument types. You might need to add explicit type casts."},
		} {
			want := fixture.ControlsReferenceDiagnostic("42883", tc.message)
			want.Hint, want.Position = tc.hint, cubeSpatialPosition(tc.q[:strings.Index(tc.q, tc.token)])
			for _, prepared := range []bool{false, true} {
				cubeError(t, p, b, c, tc.q, prepared, true, true, want)
			}
		}
	})
}
