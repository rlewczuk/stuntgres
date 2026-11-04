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

// IsEarthCubeOrigin validates the retained '(0)' literal of the released
// surface CHECK, including its exact installed cube generation.
func (v Value) IsEarthCubeOrigin(d *Descriptor) bool {
	return d != nil && d.IsCube() && v.typ == d && !v.null && v.validCube() &&
		v.cube.point && v.cube.dimension == 1 && v.cube.coordinates[0] == 0
}

// EarthCubeCheckLeaf evaluates only the selected C helper. The SQL predicate
// belongs outside that helper's entered body and contains no callback work.
func (v Value) EarthCubeCheckLeaf(k EarthCheck, origin Value) (float64, error) {
	if !k.Valid() || !v.typ.IsCube() || v.null || !v.validCube() {
		return 0, fmt.Errorf("invalid earth CHECK cube")
	}
	switch k {
	case EarthNotPoint:
		if v.cube.point {
			return 1, nil
		}
		return 0, nil
	case EarthNot3D:
		return float64(v.cube.dimension), nil
	default:
		if !origin.IsEarthCubeOrigin(v.typ) {
			return 0, fmt.Errorf("invalid earth CHECK origin")
		}
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
		return math.Sqrt(sum), nil
	}
}

func (k EarthCheck) MatchLeaf(leaf float64) bool {
	switch k {
	case EarthNotPoint:
		return leaf != 0
	case EarthNot3D:
		return leaf <= 3
	case EarthOnSurface:
		return math.Abs(leaf/EarthRadius-1) < 1e-6
	}
	return false
}
