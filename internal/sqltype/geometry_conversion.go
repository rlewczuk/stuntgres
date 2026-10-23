package sqltype

import (
	"context"
	"fmt"
	"math"
)

// GeometryConversion identifies only the twenty-one released constructor signatures.
// It does not add casts, arrays or default comparison capabilities.
type GeometryConversion uint8

const (
	BoxCircle GeometryConversion = iota
	BoxPoint
	BoxPoints
	BoxPolygon
	BoundBox
	CircleBox
	CirclePoint
	CirclePolygon
	LinePoints
	LsegBox
	LsegPoints
	PathPolygon
	PointCoordinates
	PointBox
	PointCircle
	PointLseg
	PointPolygon
	PolygonBox
	PolygonCircle
	PolygonCirclePoints
	PolygonPath
)

var geometryConversions = [...]struct {
	name    string
	formals [2]ID
	result  ID
	arity   int
}{
	{"box", [2]ID{Circle}, Box, 1},
	{"box", [2]ID{Point}, Box, 1},
	{"box", [2]ID{Point, Point}, Box, 2},
	{"box", [2]ID{Polygon}, Box, 1},
	{"bound_box", [2]ID{Box, Box}, Box, 2},
	{"circle", [2]ID{Box}, Circle, 1},
	{"circle", [2]ID{Point, Float8}, Circle, 2},
	{"circle", [2]ID{Polygon}, Circle, 1},
	{"line", [2]ID{Point, Point}, Line, 2},
	{"lseg", [2]ID{Box}, Lseg, 1},
	{"lseg", [2]ID{Point, Point}, Lseg, 2},
	{"path", [2]ID{Polygon}, Path, 1},
	{"point", [2]ID{Float8, Float8}, Point, 2},
	{"point", [2]ID{Box}, Point, 1},
	{"point", [2]ID{Circle}, Point, 1},
	{"point", [2]ID{Lseg}, Point, 1},
	{"point", [2]ID{Polygon}, Point, 1},
	{"polygon", [2]ID{Box}, Polygon, 1},
	{"polygon", [2]ID{Circle}, Polygon, 1},
	{"polygon", [2]ID{Int4, Circle}, Polygon, 2},
	{"polygon", [2]ID{Path}, Polygon, 1},
}

func (op GeometryConversion) Name() string {
	if int(op) >= len(geometryConversions) {
		return ""
	}
	return geometryConversions[op].name
}
func (op GeometryConversion) Arity() int {
	if op.Name() == "" {
		return 0
	}
	return geometryConversions[op].arity
}
func (op GeometryConversion) Formal(i int) ID { return geometryConversions[op].formals[i] }
func (op GeometryConversion) ResultID() ID    { return geometryConversions[op].result }

// Participating geometry/float descriptors carry their catalog's float8 authority.
// INT4 is authorized separately by the result polygon's canonical association.
func geometryConversionOwner(d *Descriptor) *Descriptor {
	if d == nil {
		return nil
	}
	switch d.id {
	case Float8:
		return d
	case Point:
		return d.pointFloat
	case Circle:
		return d.circleFloat
	case Box, Line, Lseg, Path:
		if d.geometryPoint != nil {
			return d.geometryPoint.pointFloat
		}
	case Polygon:
		if d.polygonPoint != nil {
			return d.polygonPoint.pointFloat
		}
	}
	return nil
}

type GeometryConversionPlan struct {
	operation GeometryConversion
	inputs    [2]*Descriptor
	result    *Descriptor
}

func BindGeometryConversion(op GeometryConversion, result Usage, inputs [2]Usage) (GeometryConversionPlan, error) {
	if op.Name() == "" || result.Type == nil || result.Type.id != op.ResultID() || result != result.Type.Usage() {
		return GeometryConversionPlan{}, fmt.Errorf("invalid geometry conversion result")
	}
	owner := geometryConversionOwner(result.Type)
	if owner == nil {
		return GeometryConversionPlan{}, fmt.Errorf("missing geometry conversion authority")
	}
	p := GeometryConversionPlan{operation: op, result: result.Type}
	for i := range op.Arity() {
		u := inputs[i]
		authorized := geometryConversionOwner(u.Type) == owner
		if op.Formal(i) == Int4 {
			authorized = result.Type.polygonInt != nil && u.Type == result.Type.polygonInt
		}
		if u.Type == nil || u.Type.id != op.Formal(i) || u != u.Type.Usage() || !authorized {
			return GeometryConversionPlan{}, fmt.Errorf("invalid geometry conversion operand")
		}
		p.inputs[i] = u.Type
	}
	return p, nil
}
func (p GeometryConversionPlan) Operation() GeometryConversion { return p.operation }
func (p GeometryConversionPlan) Evaluate(ctx context.Context, values [2]Value) (Value, error) {
	if err := ctx.Err(); err != nil {
		return Value{}, err
	}
	if p.result == nil || p.operation.Name() == "" {
		return Value{}, fmt.Errorf("invalid geometry conversion plan")
	}
	for i := range p.operation.Arity() {
		if values[i].typ != p.inputs[i] {
			return Value{}, fmt.Errorf("invalid geometry conversion value")
		}
	}
	for i := range p.operation.Arity() {
		if values[i].null {
			return Null(p.result), nil
		}
	}
	var c [4]float64
	a := values[0]
	switch p.operation {
	case PolygonBox, PolygonPath, PolygonCircle, PolygonCirclePoints:
		return p.polygonConversion(ctx, values)
	case LsegBox:
		if a.geometry == nil {
			return Value{}, fmt.Errorf("missing BOX payload")
		}
		return Value{typ: p.result, geometry: a.geometry}, nil
	case PointCoordinates:
		return pointValue(p.result, a.floating, values[1].floating), nil
	case PointCircle:
		return pointValue(p.result, a.circle.x, a.circle.y), nil
	case PathPolygon:
		if !a.validPolygon() {
			return Value{}, fmt.Errorf("missing polygon payload")
		}
		points := make([][2]float64, len(a.polygon.points))
		for i, v := range a.polygon.points {
			if err := ctx.Err(); err != nil {
				return Value{}, err
			}
			points[i] = [2]float64{v.x, v.y}
		}
		return Value{typ: p.result, path: &pathPayload{closed: true, points: points}}, nil
	case PointBox, PointLseg:
		if a.geometry == nil {
			return Value{}, fmt.Errorf("missing geometry payload")
		}
		center, err := geometryMidpoint(a.geometry.coordinates)
		if err != nil {
			return Value{}, err
		}
		return pointValue(p.result, center[0], center[1]), nil
	case BoxPoint, BoxPoints, LinePoints, LsegPoints:
		c[0], c[1] = pointCoordinates(a)
		c[2], c[3] = c[0], c[1]
		if p.operation != BoxPoint {
			c[2], c[3] = pointCoordinates(values[1])
		}
		if p.operation == LinePoints {
			return p.result.lineFromPointsWithCode(c, "22023")
		}
		if p.operation == BoxPoints {
			for i := range 2 {
				if compareFloats(c[i], c[i+2]) <= 0 {
					c[i], c[i+2] = c[i+2], c[i]
				}
			}
		}
	case BoxPolygon:
		if !a.validPolygon() {
			return Value{}, fmt.Errorf("missing polygon payload")
		}
		first := a.polygon.points[0]
		c = [4]float64{first.x, first.y, first.x, first.y}
		for _, v := range a.polygon.points[1:] {
			if err := ctx.Err(); err != nil {
				return Value{}, err
			}
			if compareFloats(v.x, c[0]) > 0 {
				c[0] = v.x
			}
			if compareFloats(v.y, c[1]) > 0 {
				c[1] = v.y
			}
			if compareFloats(v.x, c[2]) < 0 {
				c[2] = v.x
			}
			if compareFloats(v.y, c[3]) < 0 {
				c[3] = v.y
			}
		}
	case BoxCircle:
		delta, err := checkedFloatingBinary(ArithmeticDivide, Float8, a.circle.radius, math.Sqrt2)
		if err != nil {
			return Value{}, err
		}
		for i, center := range [2]float64{a.circle.x, a.circle.y} {
			c[i], err = checkedFloatingBinary(ArithmeticAdd, Float8, center, delta)
			if err != nil {
				return Value{}, err
			}
		}
		for i, center := range [2]float64{a.circle.x, a.circle.y} {
			c[i+2], err = checkedFloatingBinary(ArithmeticSubtract, Float8, center, delta)
			if err != nil {
				return Value{}, err
			}
		}
	case BoundBox:
		if a.geometry == nil || values[1].geometry == nil {
			return Value{}, fmt.Errorf("missing BOX payload")
		}
		c = a.geometry.coordinates
		other := values[1].geometry.coordinates
		for i := range 2 {
			if compareFloats(c[i], other[i]) <= 0 {
				c[i] = other[i]
			}
			if compareFloats(c[i+2], other[i+2]) >= 0 {
				c[i+2] = other[i+2]
			}
		}
	case CirclePoint:
		x, y := pointCoordinates(a)
		return Value{typ: p.result, circle: circleDatum{x: x, y: y, radius: values[1].floating}}, nil
	case CircleBox:
		if a.geometry == nil {
			return Value{}, fmt.Errorf("missing BOX payload")
		}
		c = a.geometry.coordinates
		var center [2]float64
		for i := range 2 {
			n, err := checkedFloatingBinary(ArithmeticAdd, Float8, c[i], c[i+2])
			if err != nil {
				return Value{}, err
			}
			center[i], err = checkedFloatingBinary(ArithmeticDivide, Float8, n, 2)
			if err != nil {
				return Value{}, err
			}
		}
		radius, err := geometryDistance(center[0], center[1], c[0], c[1])
		if err != nil {
			return Value{}, err
		}
		return Value{typ: p.result, circle: circleDatum{x: center[0], y: center[1], radius: radius}}, nil
	case CirclePolygon, PointPolygon:
		center, err := polygonConversionCircle(ctx, a)
		if err != nil {
			return Value{}, err
		}
		if p.operation == PointPolygon {
			return pointValue(p.result, center.x, center.y), nil
		}
		return Value{typ: p.result, circle: center}, nil
	}
	return Value{typ: p.result, geometry: &geometryPayload{coordinates: c}}, nil
}

// PostgreSQL poly_center computes the complete enclosing circle, including
// radius arithmetic that can fail even when the center itself is finite.
func polygonConversionCircle(ctx context.Context, a Value) (circleDatum, error) {
	if !a.validPolygon() {
		return circleDatum{}, fmt.Errorf("missing polygon payload")
	}
	var center circleDatum
	for _, v := range a.polygon.points {
		if err := ctx.Err(); err != nil {
			return circleDatum{}, err
		}
		var err error
		center.x, err = checkedFloatingBinary(ArithmeticAdd, Float8, center.x, v.x)
		if err != nil {
			return circleDatum{}, err
		}
		center.y, err = checkedFloatingBinary(ArithmeticAdd, Float8, center.y, v.y)
		if err != nil {
			return circleDatum{}, err
		}
	}
	n := float64(len(a.polygon.points))
	var err error
	center.x, err = checkedFloatingBinary(ArithmeticDivide, Float8, center.x, n)
	if err != nil {
		return circleDatum{}, err
	}
	center.y, err = checkedFloatingBinary(ArithmeticDivide, Float8, center.y, n)
	if err != nil {
		return circleDatum{}, err
	}
	for _, v := range a.polygon.points {
		if err := ctx.Err(); err != nil {
			return circleDatum{}, err
		}
		d, err := geometryDistance(v.x, v.y, center.x, center.y)
		if err != nil {
			return circleDatum{}, err
		}
		center.radius, err = checkedFloatingBinary(ArithmeticAdd, Float8, center.radius, d)
		if err != nil {
			return circleDatum{}, err
		}
	}
	center.radius, err = checkedFloatingBinary(ArithmeticDivide, Float8, center.radius, n)
	if err != nil {
		return circleDatum{}, err
	}
	return center, nil
}
