package sqltype

import (
	"context"
	"fmt"

	"stuntgres/internal/sqlstate"
)

// CubeOperation is the bounded installed constructor/inquiry selection.
type CubeOperation uint8

const (
	CubeScalar CubeOperation = iota
	CubeScalars
	CubeArray
	CubeArrays
	CubeExtend
	CubeExtendPair
	CubeDimension
	CubeIsPoint
	CubeLowerCoordinate
	CubeUpperCoordinate
	CubeRawCoordinate
	CubeSignedCoordinate
)

func cubeConstructionLimit(message string) error {
	return &sqlstate.Error{Severity: "ERROR", SeverityUnlocalized: "ERROR", Code: "54000", Message: message, Detail: "A cube cannot have more than 100 dimensions."}
}

// CubeInfo inspects the immutable payload without copying coordinates. The
// descriptor comparison rejects values from another installed generation.
func CubeInfo(d *Descriptor, value Value) (dimension int, point bool, err error) {
	if !d.IsCube() || value.typ != d || value.null || !value.validCube() {
		return 0, false, fmt.Errorf("invalid cube inquiry operand")
	}
	return value.cube.dimension, value.cube.point, nil
}

// ConstructCube builds a payload directly from already-coerced formal values.
// Its result owns the sole coordinate buffer; input arrays/cubes remain immutable.
func ConstructCube(ctx context.Context, d *Descriptor, operation CubeOperation, args []Value) (Value, error) {
	if !d.IsCube() {
		return Value{}, fmt.Errorf("invalid cube constructor descriptor")
	}
	count := 1
	switch operation {
	case CubeScalars, CubeArrays, CubeExtend:
		count = 2
	case CubeExtendPair:
		count = 3
	case CubeScalar, CubeArray:
	default:
		return Value{}, fmt.Errorf("invalid cube constructor operation")
	}
	if len(args) != count {
		return Value{}, fmt.Errorf("invalid cube constructor arity")
	}
	for i, arg := range args {
		valid := arg.typ != nil && arg.typ.ID() == Float8
		if operation == CubeArray || operation == CubeArrays {
			valid = arg.typ != nil && arg.typ.IsArray() && arg.typ.Element().ID() == Float8 && (arg.null || arg.array != nil)
		} else if (operation == CubeExtend || operation == CubeExtendPair) && i == 0 {
			valid = arg.typ == d && (arg.null || arg.validCube())
		}
		if !valid {
			return Value{}, fmt.Errorf("invalid cube constructor operand")
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
	n, point := 1, true
	switch operation {
	case CubeScalar, CubeScalars:
		scratch[0] = args[0].floating
		if operation == CubeScalars {
			scratch[1] = args[1].floating
			point = scratch[0] == scratch[1]
		}
	case CubeArray, CubeArrays:
		// Both arrays' NULL elements precede every dimension/count check.
		for _, array := range args {
			for i := range array.ArrayLen() {
				if err := ctx.Err(); err != nil {
					return Value{}, err
				}
				if array.ArrayElement(i).null {
					return Value{}, failure("2202E", "cannot work with arrays containing NULLs")
				}
			}
		}
		n = args[0].ArrayLen()
		if n > 100 {
			message := "array is too long"
			if operation == CubeArrays {
				message = "can't extend cube"
			}
			return Value{}, cubeConstructionLimit(message)
		}
		if operation == CubeArrays && args[1].ArrayLen() != n {
			return Value{}, failure("2202E", "UR and LL arrays must be of same length")
		}
		for i := range n {
			scratch[i] = args[0].ArrayElement(i).floating
			if operation == CubeArrays {
				scratch[n+i] = args[1].ArrayElement(i).floating
				point = point && scratch[i] == scratch[n+i]
			}
		}
	case CubeExtend, CubeExtendPair:
		base := args[0].cube
		n, point = base.dimension+1, base.point
		if n > 100 {
			return Value{}, cubeConstructionLimit("can't extend cube")
		}
		copy(scratch[:], base.coordinates[:base.dimension])
		scratch[n-1] = args[1].floating
		if operation == CubeExtendPair {
			point = point && args[1].floating == args[2].floating
		}
		if !point {
			second := base.coordinates[:base.dimension]
			if !base.point {
				second = base.coordinates[base.dimension:]
			}
			copy(scratch[n:], second)
			scratch[2*n-1] = args[1].floating
			if operation == CubeExtendPair {
				scratch[2*n-1] = args[2].floating
			}
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
