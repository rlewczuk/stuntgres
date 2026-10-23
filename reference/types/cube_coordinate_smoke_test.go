package reference_test

import (
	"database/sql"
	"math"
	"testing"

	"stuntgres/reference/fixture"
)

func TestReferenceCubeCoordinateFeatureSmoke(t *testing.T) {
	cubeScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
		cubeCoordinateStoredSetup(t, p, b, c, false)
		cubeSpatialBoth(t, p, b, c, cubeCoordinateTable(b, false))
		q := "SELECT " + cubeCoordinateProjection("$1::cube", "$2::integer")
		held := cubeCoordinateHeld(t, p, c, q)
		for _, tc := range []struct {
			c, n any
			want []any
		}{
			{"(3,1),(1,4)", 1, []any{float64(1), float64(3), float64(3), float64(1)}},
			{"(42,137)", 2, []any{float64(137), float64(137), float64(137), float64(42)}},
			{nil, 0, []any{nil, nil, nil, nil}},
		} {
			fixture.RunStep(t, p.Ctx, held, b.Recorder, cubeCoordinateSelect(b, q, [][]any{tc.want}, tc.c, tc.n))
		}
		projection := "SELECT $1::cube ~> $2::integer AS v"
		knn := cubeCoordinateHeld(t, p, c, projection)
		for _, tc := range []struct {
			n int64
			v float64
		}{{-2, -3}, {5, 0}, {-5, math.Copysign(0, -1)}, {-2147483648, math.Copysign(0, -1)}} {
			fixture.RunStep(t, p.Ctx, knn, b.Recorder, cubeCoordinateScalar(b, projection, tc.v, "(3,1),(1,4)", tc.n))
		}
		for _, prepared := range []bool{false, true} {
			cubeError(t, p, b, c, "SELECT '(1)'::cube -> 0", prepared, true, false, fixture.ControlsReferenceDiagnostic("2202E", "cube index 0 is out of bounds"))
			cubeError(t, p, b, c, "SELECT '(1)'::cube ~> 0", prepared, true, false, fixture.ControlsReferenceDiagnostic("2202E", "zero cube index is not defined"))
		}
		cubeCoordinateControlCycle(t, p, b, c, false)
	})
}
