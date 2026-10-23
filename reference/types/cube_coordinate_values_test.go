package reference_test

import (
	"database/sql"
	"fmt"
	"math"
	"strings"
	"testing"

	"stuntgres/reference/fixture"
)

func TestReferenceCubeCoordinateValues(t *testing.T) {
	cubeScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
		cubeInstall(t, p, b, c)
		run := func(s fixture.Step) { cubeSpatialBoth(t, p, b, c, s) }
		for _, tc := range []struct {
			n                             int
			lower, upper, raw, projection float64
		}{
			{1, 1, 3, 3, 1}, {2, 1, 4, 1, 3}, {3, 0, 0, 1, 1}, {4, 0, 0, 4, 4},
		} {
			q := "SELECT " + cubeCoordinateProjection("'(3,1),(1,4)'::cube", fmt.Sprint(tc.n))
			run(cubeCoordinateSelect(b, q, [][]any{{tc.lower, tc.upper, tc.raw, tc.projection}}))
		}
		for _, tc := range []struct {
			c     string
			n     int
			value float64
		}{
			{"(42,137)", 1, 42}, {"(42,137)", 2, 137}, {"(42,137)", 3, 42}, {"(42,137)", 4, 137},
			{"(7),(2)", 1, 7}, {"(7),(2)", 2, 2},
		} {
			run(cubeCoordinateScalar(b, fmt.Sprintf("SELECT %s::cube -> %d AS v", cubeLiteral(tc.c), tc.n), tc.value))
		}
		for _, n := range []int{0, -1, 3, 2147483647, -2147483648} {
			for _, name := range []string{"cube_ll_coord", "cube_ur_coord"} {
				run(cubeCoordinateScalar(b, fmt.Sprintf("SELECT %s('(3,1),(1,4)'::cube,(%d)::integer) AS v", name, n), 0))
				run(cubeCoordinateScalar(b, fmt.Sprintf("SELECT %s('()'::cube,(%d)::integer) AS v", name, n), 0))
			}
		}
		for _, tc := range []struct {
			n     int64
			value float64
		}{{1, 1}, {2, 3}, {3, 1}, {4, 4}, {5, 0}, {-1, -1}, {-2, -3}, {-3, -1}, {-4, -4}, {-5, math.Copysign(0, -1)}, {2147483647, 0}, {-2147483647, math.Copysign(0, -1)}, {-2147483648, math.Copysign(0, -1)}} {
			// INT_MIN independently expected by safe projection semantics; root's
			// isolated pinned18.6 probe observed bits 8000000000000000.
			for _, expr := range []string{"$1::cube ~> $2::integer", "cube_coord_llur($1::cube,$2::integer)"} {
				run(cubeCoordinateScalar(b, "SELECT "+expr+" AS v", tc.value, "(3,1),(1,4)", tc.n))
			}
		}
		for _, tc := range []struct {
			expr  string
			value float64
		}{
			{"'()'::cube ~> 1", 0}, {"'()'::cube ~> -1", math.Copysign(0, -1)},
			{"cube_coord('(3,1),(1,4)'::cube,1)", 3},
			{"cube_ll_coord('(7),(2)'::cube,1)", 2}, {"cube_ur_coord('(7),(2)'::cube,1)", 7},
			{"'(-0,1),(0,2)'::cube -> 1", math.Copysign(0, -1)},
			{"cube_ll_coord('(-0,1),(0,2)'::cube,1)", 0}, {"cube_ur_coord('(-0,1),(0,2)'::cube,1)", 0},
			{"cube_ll_coord('(0,2),(-0,1)'::cube,1)", math.Copysign(0, -1)},
			{"cube_ur_coord('(0,2),(-0,1)'::cube,1)", math.Copysign(0, -1)},
			{"'(0,2),(-0,1)'::cube ~> -1", 0},
			{"cube_ll_coord('(NaN),(1)'::cube,1)", 1}, {"cube_ur_coord('(NaN),(1)'::cube,1)", 1},
			{"cube_ll_coord('(1),(NaN)'::cube,1)", math.NaN()}, {"cube_ur_coord('(1),(NaN)'::cube,1)", math.NaN()},
			{"'(NaN),(1)'::cube -> 1", math.NaN()},
			{"'(1),(Infinity)'::cube ~> -2", math.Inf(-1)},
			{"'(-Infinity),(1)'::cube ~> -1", math.Inf(1)},
			{"'(0.10000000000000002)'::cube -> 1", 0.10000000000000002},
			{"'(5e-324)'::cube -> 2", math.SmallestNonzeroFloat64},
			{"'(1.7976931348623157e308)'::cube ~> 2", math.MaxFloat64},
		} {
			q := "SELECT " + tc.expr + " AS v"
			if math.IsNaN(tc.value) {
				cubeCoordinateNaNBoth(t, p, b, c, q)
			} else {
				run(cubeCoordinateScalar(b, q, tc.value))
			}
		}
		point := "(" + strings.TrimSuffix(strings.Repeat("7,", 100), ",") + ")"
		for _, tc := range []struct {
			expr string
			want float64
		}{
			{"$1::cube -> 200", 7}, {"$1::cube ~> 199", 7}, {"$1::cube ~> 200", 7}, {"$1::cube ~> 201", 0}, {"cube_ll_coord($1::cube,100)", 7}, {"cube_ur_coord($1::cube,101)", 0},
		} {
			run(cubeCoordinateScalar(b, "SELECT "+tc.expr+" AS v", tc.want, point))
		}
		fixture.RunStep(t, p.Ctx, c, b.Recorder, fixture.Exec("CREATE TABLE cube_coordinate_sort(id integer,v cube)", 0))
		fixture.RunStep(t, p.Ctx, c, b.Recorder, fixture.Exec("INSERT INTO cube_coordinate_sort VALUES (1,'(3)'),(2,'(1)'),(3,'(2)')", 3))
		for _, order := range []string{"v ~> 1 ASC,id", "v ~> -1 DESC,id"} {
			run(fixture.Select("SELECT id FROM cube_coordinate_sort ORDER BY "+order, []string{"id"}, []string{"INT4"}, [][]any{{int64(2)}, {int64(3)}, {int64(1)}}))
		}
	})
}
