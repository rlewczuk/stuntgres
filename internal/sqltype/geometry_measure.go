package sqltype

import (
	"context"
	"fmt"
	"math"
)

// GeometryMeasure names the selected unary and binary measurements and path helpers.
// Radius retains its existing CirclePlan owner.
type GeometryMeasure uint8

const (
	AreaBox GeometryMeasure = iota
	AreaPath
	AreaCircle
	CenterBox
	CenterCircle
	DiameterCircle
	WidthBox
	HeightBox
	DiagonalBox
	SlopePoints
	IsClosedPath
	IsOpenPath
	ClosePath
	OpenPath
	LengthLseg
	LengthPath
	NPointsPath
	NPointsPolygon
)

var geometryMeasures = [...]struct {
	name    string
	formals [2]ID
	result  ID
	arity   int
}{
	{"area", [2]ID{Box}, Float8, 1},
	{"area", [2]ID{Path}, Float8, 1},
	{"area", [2]ID{Circle}, Float8, 1},
	{"center", [2]ID{Box}, Point, 1},
	{"center", [2]ID{Circle}, Point, 1},
	{"diameter", [2]ID{Circle}, Float8, 1},
	{"width", [2]ID{Box}, Float8, 1},
	{"height", [2]ID{Box}, Float8, 1},
	{"diagonal", [2]ID{Box}, Lseg, 1},
	{"slope", [2]ID{Point, Point}, Float8, 2},
	{"isclosed", [2]ID{Path}, Bool, 1},
	{"isopen", [2]ID{Path}, Bool, 1},
	{"pclose", [2]ID{Path}, Path, 1},
	{"popen", [2]ID{Path}, Path, 1},
	{"length", [2]ID{Lseg}, Float8, 1},
	{"length", [2]ID{Path}, Float8, 1},
	{"npoints", [2]ID{Path}, Int4, 1},
	{"npoints", [2]ID{Polygon}, Int4, 1},
}

func (op GeometryMeasure) Name() string {
	if int(op) >= len(geometryMeasures) {
		return ""
	}
	return geometryMeasures[op].name
}
func (op GeometryMeasure) Arity() int {
	if op.Name() == "" {
		return 0
	}
	return geometryMeasures[op].arity
}
func (op GeometryMeasure) Formal(i int) ID { return geometryMeasures[op].formals[i] }
func (op GeometryMeasure) ResultID() ID    { return geometryMeasures[op].result }

type GeometryMeasurePlan struct {
	operation GeometryMeasure
	inputs    [2]*Descriptor
	result    *Descriptor
}

func BindGeometryMeasure(op GeometryMeasure, result Usage, inputs [2]Usage) (GeometryMeasurePlan, error) {
	if op.Name() == "" || result.Type == nil || result.Type.id != op.ResultID() || result != result.Type.Usage() {
		return GeometryMeasurePlan{}, fmt.Errorf("invalid geometry measurement result")
	}
	owner := geometryConversionOwner(result.Type)
	if input := inputs[0].Type; input != nil {
		var scalar *Descriptor
		switch result.Type.id {
		case Bool:
			if input.id == Path && input.geometryPoint != nil {
				scalar = input.geometryPoint.pointBoolean
			}
		case Int4:
			if input.id == Path {
				scalar = input.pathInt
			} else if input.id == Polygon {
				scalar = input.polygonInt
			}
		}
		if scalar != nil && result.Type == scalar {
			owner = geometryConversionOwner(input)
		}
	}
	if owner == nil || op.Arity() == 1 && inputs[1] != (Usage{}) {
		return GeometryMeasurePlan{}, fmt.Errorf("invalid geometry measurement authority or arity")
	}
	p := GeometryMeasurePlan{operation: op, result: result.Type}
	for i := range op.Arity() {
		u := inputs[i]
		if u.Type == nil || u.Type.id != op.Formal(i) || u != u.Type.Usage() || geometryConversionOwner(u.Type) != owner {
			return GeometryMeasurePlan{}, fmt.Errorf("invalid geometry measurement operand")
		}
		p.inputs[i] = u.Type
	}
	return p, nil
}

func (p GeometryMeasurePlan) Operation() GeometryMeasure { return p.operation }
func (p GeometryMeasurePlan) Evaluate(ctx context.Context, values [2]Value) (Value, error) {
	if err := ctx.Err(); err != nil {
		return Value{}, err
	}
	if p.result == nil || p.operation.Name() == "" {
		return Value{}, fmt.Errorf("invalid geometry measurement plan")
	}
	for i := range p.operation.Arity() {
		if p.inputs[i] == nil || values[i].typ != p.inputs[i] {
			return Value{}, fmt.Errorf("invalid geometry measurement value")
		}
	}
	for i := range p.operation.Arity() {
		if values[i].null {
			return Null(p.result), nil
		}
	}
	a := values[0]
	var n float64
	var err error
	switch p.operation {
	case IsClosedPath, IsOpenPath, ClosePath, OpenPath, LengthPath, NPointsPath:
		if a.path == nil || len(a.path.points) == 0 {
			return Value{}, fmt.Errorf("missing PATH payload")
		}
		switch p.operation {
		case IsClosedPath, IsOpenPath:
			closed := a.path.closed
			if p.operation == IsOpenPath {
				closed = !closed
			}
			return Value{typ: p.result, boolean: closed}, nil
		case NPointsPath:
			return Value{typ: p.result, integer: int64(len(a.path.points))}, nil
		case ClosePath, OpenPath:
			closed := p.operation == ClosePath
			if a.path.closed == closed {
				return a, nil
			}
			// Published coordinates are immutable; only the closure header changes.
			return Value{typ: p.result, path: &pathPayload{closed: closed, points: a.path.points}}, nil
		case LengthPath:
			points := a.path.points
			if a.path.closed {
				last, first := points[len(points)-1], points[0]
				n, err = geometryDistance(last[0], last[1], first[0], first[1])
				if err != nil {
					return Value{}, err
				}
			}
			for i := range len(points) - 1 {
				if err := ctx.Err(); err != nil {
					return Value{}, err
				}
				a, b := points[i], points[i+1]
				distance, e := geometryDistance(a[0], a[1], b[0], b[1])
				if e != nil {
					return Value{}, e
				}
				n, err = checkedFloatingBinary(ArithmeticAdd, Float8, n, distance)
				if err != nil {
					return Value{}, err
				}
			}
		}
	case NPointsPolygon:
		if a.polygon == nil || len(a.polygon.points) == 0 {
			return Value{}, fmt.Errorf("missing POLYGON payload")
		}
		return Value{typ: p.result, integer: int64(len(a.polygon.points))}, nil
	case LengthLseg:
		if a.geometry == nil {
			return Value{}, fmt.Errorf("missing LSEG payload")
		}
		c := a.geometry.coordinates
		n, err = geometryDistance(c[0], c[1], c[2], c[3])
	case WidthBox, HeightBox, DiagonalBox:
		if a.geometry == nil {
			return Value{}, fmt.Errorf("missing BOX payload")
		}
		if p.operation == DiagonalBox {
			return Value{typ: p.result, geometry: a.geometry}, nil
		}
		axis := 0
		if p.operation == HeightBox {
			axis = 1
		}
		n, err = checkedFloatingBinary(ArithmeticSubtract, Float8, a.geometry.coordinates[axis], a.geometry.coordinates[axis+2])
	case SlopePoints:
		x1, y1 := pointCoordinates(a)
		x2, y2 := pointCoordinates(values[1])
		if boxFuzzyEqual(x1, x2) {
			n = math.Inf(1)
		} else if boxFuzzyEqual(y1, y2) {
			n = 0
		} else {
			dy, e := checkedFloatingBinary(ArithmeticSubtract, Float8, y1, y2)
			if e != nil {
				return Value{}, e
			}
			dx, e := checkedFloatingBinary(ArithmeticSubtract, Float8, x1, x2)
			if e != nil {
				return Value{}, e
			}
			n, err = checkedFloatingBinary(ArithmeticDivide, Float8, dy, dx)
		}
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
