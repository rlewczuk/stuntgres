package sqltype

import (
	"context"
	"fmt"
)

// TransformCube consumes exact, already-coerced formals and owns its result's
// coordinate buffer. Ordered comparisons deliberately match the released C code.
func TransformCube(ctx context.Context, d *Descriptor, operation CubeOperation, args []Value) (Value, error) {
	if !d.IsCube() {
		return Value{}, fmt.Errorf("invalid cube transform descriptor")
	}
	count := 2
	switch operation {
	case CubeSubset, CubeUnion, CubeIntersection:
	case CubeEnlarge:
		count = 3
	default:
		return Value{}, fmt.Errorf("invalid cube transform operation")
	}
	if len(args) != count {
		return Value{}, fmt.Errorf("invalid cube transform arity")
	}
	for i, arg := range args {
		valid := arg.typ == d && (arg.null || arg.validCube())
		if operation == CubeSubset && i == 1 {
			valid = arg.typ != nil && arg.typ.ID() == 1007 && arg.typ.IsArray() && arg.typ.Element().ID() == Int4 && (arg.null || arg.array != nil)
		} else if operation == CubeEnlarge && i > 0 {
			id := Float8
			if i == 2 {
				id = Int4
			}
			valid = arg.typ != nil && arg.typ.ID() == id
		}
		if !valid {
			return Value{}, fmt.Errorf("invalid cube transform operand")
		}
	}
	if err := ctx.Err(); err != nil {
		return Value{}, err
	}
	for _, arg := range args {
		if arg.null {
			return Null(d), nil
		}
	}
	var scratch [200]float64
	a := args[0].cube
	n, point := a.dimension, true
	switch operation {
	case CubeSubset:
		indexes := args[1]
		for i := range indexes.ArrayLen() {
			if err := ctx.Err(); err != nil {
				return Value{}, err
			}
			if indexes.ArrayElement(i).null {
				return Value{}, failure("2202E", "cannot work with arrays containing NULLs")
			}
		}
		n, point = indexes.ArrayLen(), a.point
		if n > 100 {
			return Value{}, cubeConstructionLimit("array is too long")
		}
		for i := range n {
			index := indexes.ArrayElement(i).integer
			if index <= 0 || index > int64(a.dimension) {
				return Value{}, failure("2202E", "Index out of bounds")
			}
			scratch[i], scratch[n+i] = a.endpoints(int(index - 1))
		}
	case CubeUnion, CubeIntersection:
		b := args[1].cube
		if a.dimension < b.dimension {
			a, b = b, a
		}
		n = a.dimension
		for i := range n {
			if err := ctx.Err(); err != nil {
				return Value{}, err
			}
			a1, a2 := a.endpoints(i)
			var lower, upper float64
			if i < b.dimension {
				b1, b2 := b.endpoints(i)
				if operation == CubeUnion {
					lower = cubeMinimum(cubeMinimum(a1, a2), cubeMinimum(b1, b2))
					upper = cubeMaximum(cubeMaximum(a1, a2), cubeMaximum(b1, b2))
				} else {
					lower = cubeMaximum(cubeMinimum(a1, a2), cubeMinimum(b1, b2))
					upper = cubeMinimum(cubeMaximum(a1, a2), cubeMaximum(b1, b2))
				}
			} else if operation == CubeUnion {
				lower = cubeMinimum(0, cubeMinimum(a1, a2))
				upper = cubeMaximum(0, cubeMaximum(a1, a2))
			} else {
				lower = cubeMaximum(0, cubeMinimum(a1, a2))
				upper = cubeMinimum(0, cubeMaximum(a1, a2))
			}
			scratch[i], scratch[n+i] = lower, upper
			point = point && lower == upper
		}
	case CubeEnlarge:
		radius, requested := args[1].floating, args[2].integer
		if requested > 100 {
			requested = 100
		}
		if radius > 0 && requested > int64(n) {
			n = int(requested)
		}
		for i := range n {
			if err := ctx.Err(); err != nil {
				return Value{}, err
			}
			lower, upper := -radius, radius
			if i < a.dimension {
				ll, ur := a.endpoints(i)
				if ll >= ur {
					lower, upper = ur-radius, ll+radius
				} else {
					lower, upper = ll-radius, ur+radius
				}
				if lower > upper {
					lower = (lower + upper) / 2
					upper = lower
				}
			}
			scratch[i], scratch[n+i] = lower, upper
			point = point && lower == upper
		}
	}
	coordinates := n
	if !point {
		coordinates *= 2
	}
	owned := make([]float64, coordinates)
	copy(owned, scratch[:coordinates])
	return Value{typ: d, cube: &cubePayload{dimension: n, point: point, coordinates: owned}}, nil
}
