package sqltype

import (
	"context"
	"fmt"
	"math"
)

// CubeSpatialOperation identifies only the six installed binary cube routines.
type CubeSpatialOperation uint8

const (
	CubeOverlap CubeSpatialOperation = iota
	CubeContains
	CubeContained
	CubeDistance
	CubeTaxicab
	CubeChebyshev
)

var cubeSpatialRoutines = [...]string{"cube_overlap", "cube_contains", "cube_contained", "cube_distance", "distance_taxicab", "distance_chebyshev"}

func (o CubeSpatialOperation) Routine() string {
	if int(o) >= len(cubeSpatialRoutines) {
		return ""
	}
	return cubeSpatialRoutines[o]
}
func CubeSpatialKind(name string) (CubeSpatialOperation, bool) {
	for i, routine := range cubeSpatialRoutines {
		if name == routine {
			return CubeSpatialOperation(i), true
		}
	}
	return 0, false
}
func (o CubeSpatialOperation) ResultID() ID {
	if o <= CubeContained {
		return Bool
	}
	return Float8
}

type CubeSpatialPlan struct {
	operation     CubeSpatialOperation
	input, result *Descriptor
}

func BindCubeSpatial(op CubeSpatialOperation, result, left, right Usage) (CubeSpatialPlan, error) {
	if op.Routine() == "" || !left.Type.IsCube() || left != left.Type.Usage() || right != left || result.Type == nil || result.Type.ID() != op.ResultID() || result != result.Type.Usage() {
		return CubeSpatialPlan{}, fmt.Errorf("invalid cube spatial signature")
	}
	return CubeSpatialPlan{op, left.Type, result.Type}, nil
}
func (p CubeSpatialPlan) Operation() CubeSpatialOperation { return p.operation }

// These are the ordered C Min/Max expressions, not IEEE-propagating math.Min/Max.
func cubeMinimum(a, b float64) float64 {
	if a < b {
		return a
	}
	return b
}
func cubeMaximum(a, b float64) float64 {
	if a > b {
		return a
	}
	return b
}
func (p *cubePayload) endpoints(i int) (float64, float64) {
	if i >= p.dimension {
		return 0, 0
	}
	a := p.coordinates[i]
	if p.point {
		return a, a
	}
	return a, p.coordinates[p.dimension+i]
}
func cubeAxisDistance(a1, a2, b1, b2 float64) float64 {
	if a1 <= b1 && a1 <= b2 && a2 <= b1 && a2 <= b2 {
		return cubeMinimum(b1, b2) - cubeMaximum(a1, a2)
	}
	if a1 > b1 && a1 > b2 && a2 > b1 && a2 > b2 {
		return cubeMinimum(a1, a2) - cubeMaximum(b1, b2)
	}
	return 0
}
func (p CubeSpatialPlan) Evaluate(ctx context.Context, values [2]Value) (Value, error) {
	if err := ctx.Err(); err != nil {
		return Value{}, err
	}
	if p.input == nil || p.result == nil || p.operation.Routine() == "" {
		return Value{}, fmt.Errorf("invalid cube spatial plan")
	}
	for _, v := range values {
		if v.typ != p.input || !v.null && !v.validCube() {
			return Value{}, fmt.Errorf("invalid cube spatial operand")
		}
	}
	for _, v := range values {
		if v.null {
			return Null(p.result), nil
		}
	}
	a, b := values[0].cube, values[1].cube
	op := p.operation
	if op == CubeContained {
		a, b = b, a
		op = CubeContains
	}
	if op == CubeContains {
		for i := a.dimension; i < b.dimension; i++ {
			if err := ctx.Err(); err != nil {
				return Value{}, err
			}
			b1, b2 := b.endpoints(i)
			if b1 != 0 || b2 != 0 {
				return Boolean(p.result, false), nil
			}
		}
		for i := range min(a.dimension, b.dimension) {
			if err := ctx.Err(); err != nil {
				return Value{}, err
			}
			a1, a2 := a.endpoints(i)
			b1, b2 := b.endpoints(i)
			if cubeMinimum(a1, a2) > cubeMinimum(b1, b2) || cubeMaximum(a1, a2) < cubeMaximum(b1, b2) {
				return Boolean(p.result, false), nil
			}
		}
		// Released containment ignores the container's excess dimensions.
		return Boolean(p.result, true), nil
	}
	if a.dimension < b.dimension {
		a, b = b, a
	}
	var sum float64
	for i := range a.dimension {
		if err := ctx.Err(); err != nil {
			return Value{}, err
		}
		a1, a2 := a.endpoints(i)
		b1, b2 := b.endpoints(i)
		if op == CubeOverlap {
			if cubeMinimum(a1, a2) > cubeMaximum(b1, b2) || cubeMaximum(a1, a2) < cubeMinimum(b1, b2) {
				return Boolean(p.result, false), nil
			}
			continue
		}
		d := cubeAxisDistance(a1, a2, b1, b2)
		switch op {
		case CubeDistance:
			// Keep two rounded operations, not a fused multiply/add.
			square := float64(d * d)
			sum += square
		case CubeTaxicab:
			sum += math.Abs(d)
		case CubeChebyshev:
			if d = math.Abs(d); d > sum {
				sum = d
			}
		}
	}
	if op == CubeOverlap {
		return Boolean(p.result, true), nil
	}
	if op == CubeDistance {
		sum = math.Sqrt(sum)
	}
	return Float(p.result, sum)
}
