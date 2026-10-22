package sqltype

import (
	"fmt"
	"math"
)

// EarthCheck identifies the three released earth domain CHECK recipes. It does
// not grant SQL cube operations or reinterpret the immutable cube payload.
type EarthCheck uint8

const (
	EarthNotPoint EarthCheck = iota + 1
	EarthNot3D
	EarthOnSurface
)

func (k EarthCheck) Valid() bool { return k >= EarthNotPoint && k <= EarthOnSurface }

// CheckEarthCube accepts only a non-NULL physical cube. NULL truth belongs to
// the ordinary domain expression/evaluator, not a second constraint authority.
func (v Value) CheckEarthCube(k EarthCheck) (bool, error) {
	if !k.Valid() || !v.typ.IsCube() || v.null || !v.validCube() {
		return false, fmt.Errorf("invalid earth CHECK cube")
	}
	switch k {
	case EarthNotPoint:
		return v.cube.point, nil
	case EarthNot3D:
		return v.cube.dimension <= 3, nil
	default:
		// Released cube distance_1D uses ordered comparisons. NaN contributes
		// zero; a conventional norm or Hypot would change domain acceptance.
		sum := 0.0
		for i := range v.cube.dimension {
			x := v.cube.coordinates[i]
			y := x
			if !v.cube.point {
				y = v.cube.coordinates[v.cube.dimension+i]
			}
			if x > y {
				x, y = y, x
			}
			d := 0.0
			if y <= 0 {
				d = -y
			} else if x > 0 {
				d = x
			}
			sum += d * d
		}
		return math.Abs(math.Sqrt(sum)/6378168-1) < 1e-6, nil
	}
}
