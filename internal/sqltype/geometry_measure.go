package sqltype

import (
	"context"
	"fmt"
	"math"
)

// GeometryMeasure names only the six selected measurements. Radius retains
// its existing CirclePlan owner; adjacent path and box helpers are separate.
type GeometryMeasure uint8

const (
	AreaBox GeometryMeasure = iota
	AreaPath
	AreaCircle
	CenterBox
	CenterCircle
	DiameterCircle
)

var geometryMeasures = [...]struct {
	name           string
	formal, result ID
}{
	{"area", Box, Float8},
	{"area", Path, Float8},
	{"area", Circle, Float8},
	{"center", Box, Point},
	{"center", Circle, Point},
	{"diameter", Circle, Float8},
}

func (op GeometryMeasure) Name() string {
	if int(op) >= len(geometryMeasures) {
		return ""
	}
	return geometryMeasures[op].name
}
func (op GeometryMeasure) Formal() ID   { return geometryMeasures[op].formal }
func (op GeometryMeasure) ResultID() ID { return geometryMeasures[op].result }

type GeometryMeasurePlan struct {
	operation     GeometryMeasure
	input, result *Descriptor
}

func BindGeometryMeasure(op GeometryMeasure, result, input Usage) (GeometryMeasurePlan, error) {
	if op.Name() == "" || result.Type == nil || result.Type.id != op.ResultID() || result != result.Type.Usage() {
		return GeometryMeasurePlan{}, fmt.Errorf("invalid geometry measurement result")
	}
	owner := geometryConversionOwner(result.Type)
	if owner == nil || input.Type == nil || input.Type.id != op.Formal() || input != input.Type.Usage() || geometryConversionOwner(input.Type) != owner {
		return GeometryMeasurePlan{}, fmt.Errorf("invalid geometry measurement operand")
	}
	return GeometryMeasurePlan{op, input.Type, result.Type}, nil
}

func (p GeometryMeasurePlan) Operation() GeometryMeasure { return p.operation }
func (p GeometryMeasurePlan) Evaluate(ctx context.Context, a Value) (Value, error) {
	if err := ctx.Err(); err != nil {
		return Value{}, err
	}
	if p.result == nil || p.input == nil || p.operation.Name() == "" || a.typ != p.input {
		return Value{}, fmt.Errorf("invalid geometry measurement value")
	}
	if a.null {
		return Null(p.result), nil
	}
	var n float64
	var err error
	switch p.operation {
	case AreaBox:
		n, err = boxArea(a)
	case AreaCircle:
		n, err = circleArea(a.circle.radius)
	case DiameterCircle:
		n, err = checkedFloatingBinary(ArithmeticMultiply, Float8, a.circle.radius, 2)
	case CenterCircle:
		return pointValue(p.result, a.circle.x, a.circle.y), nil
	case CenterBox:
		if a.geometry == nil {
			return Value{}, fmt.Errorf("missing BOX payload")
		}
		center, err := geometryMidpoint(a.geometry.coordinates)
		if err != nil {
			return Value{}, err
		}
		return pointValue(p.result, center[0], center[1]), nil
	case AreaPath:
		if a.path == nil || len(a.path.points) == 0 {
			return Value{}, fmt.Errorf("missing PATH payload")
		}
		if !a.path.closed {
			return Null(p.result), nil
		}
		// Preserve path_area's sequential checked operations. Regrouping cross
		// products can hide an intermediate overflow or underflow.
		for i, point := range a.path.points {
			if err := ctx.Err(); err != nil {
				return Value{}, err
			}
			j := i + 1
			if j == len(a.path.points) {
				j = 0
			}
			next := a.path.points[j]
			product, err := checkedFloatingBinary(ArithmeticMultiply, Float8, point[0], next[1])
			if err != nil {
				return Value{}, err
			}
			n, err = checkedFloatingBinary(ArithmeticAdd, Float8, n, product)
			if err != nil {
				return Value{}, err
			}
			product, err = checkedFloatingBinary(ArithmeticMultiply, Float8, point[1], next[0])
			if err != nil {
				return Value{}, err
			}
			n, err = checkedFloatingBinary(ArithmeticSubtract, Float8, n, product)
			if err != nil {
				return Value{}, err
			}
		}
		n, err = checkedFloatingBinary(ArithmeticDivide, Float8, math.Abs(n), 2)
	}
	if err != nil {
		return Value{}, err
	}
	return Value{typ: p.result, floating: n}, nil
}

// BOX center and point(BOX/LSEG) share the released checked sum-then-half order.
func geometryMidpoint(c [4]float64) ([2]float64, error) {
	var center [2]float64
	for i := range 2 {
		n, err := checkedFloatingBinary(ArithmeticAdd, Float8, c[i], c[i+2])
		if err != nil {
			return [2]float64{}, err
		}
		center[i], err = checkedFloatingBinary(ArithmeticDivide, Float8, n, 2)
		if err != nil {
			return [2]float64{}, err
		}
	}
	return center, nil
}
