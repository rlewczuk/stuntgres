package sqltype

import (
	"context"
	"fmt"
)

// CubeCoordinate selects directly from an immutable, installed cube payload.
// NULL handling belongs to the strict callable, not this non-NULL primitive.
func CubeCoordinate(ctx context.Context, d *Descriptor, value Value, operation CubeOperation, index int32) (float64, error) {
	if err := ctx.Err(); err != nil {
		return 0, err
	}
	if !d.IsCube() || value.typ != d || value.null || !value.validCube() {
		return 0, fmt.Errorf("invalid cube inquiry operand")
	}
	p := value.cube
	switch operation {
	case CubeLowerCoordinate, CubeUpperCoordinate:
		if index <= 0 || int64(index) > int64(p.dimension) {
			return 0, nil
		}
		a, b := p.endpoints(int(index) - 1)
		if operation == CubeLowerCoordinate {
			return cubeMinimum(a, b), nil
		}
		return cubeMaximum(a, b), nil
	case CubeRawCoordinate:
		if index <= 0 || int64(index) > 2*int64(p.dimension) {
			return 0, failure("2202E", fmt.Sprintf("cube index %d is out of bounds", index))
		}
		i := int(index) - 1
		if p.point {
			i %= p.dimension
		}
		return p.coordinates[i], nil
	case CubeSignedCoordinate:
		if index == 0 {
			return 0, failure("2202E", "zero cube index is not defined")
		}
		// Widen before negation: INT_MIN is a padded negative projection.
		n := int64(index)
		if n < 0 {
			n = -n
		}
		var result float64
		if axis := (n - 1) / 2; axis < int64(p.dimension) {
			a, b := p.endpoints(int(axis))
			if n%2 == 1 {
				result = cubeMinimum(a, b)
			} else {
				result = cubeMaximum(a, b)
			}
		}
		if index < 0 {
			result = -result
		}
		return result, nil
	default:
		return 0, fmt.Errorf("invalid cube coordinate operation")
	}
}
