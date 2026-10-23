package sqltype

import (
	"context"
	"fmt"
	"math"

	"stuntgres/internal/sqlstate"
)

func (p GeometryConversionPlan) polygonConversion(ctx context.Context, values [2]Value) (Value, error) {
	a := values[0]
	switch p.operation {
	case PolygonBox:
		if a.geometry == nil {
			return Value{}, fmt.Errorf("missing BOX payload")
		}
		c := a.geometry.coordinates
		return Value{typ: p.result, polygon: &polygonPayload{points: []polygonPoint{
			{c[2], c[3]}, {c[2], c[1]}, {c[0], c[1]}, {c[0], c[3]},
		}}}, nil
	case PolygonPath:
		if a.path == nil || len(a.path.points) == 0 {
			return Value{}, fmt.Errorf("missing PATH payload")
		}
		if !a.path.closed {
			return Value{}, failure("22023", "open path cannot be converted to polygon")
		}
		points := make([]polygonPoint, len(a.path.points))
		for i, point := range a.path.points {
			if err := ctx.Err(); err != nil {
				return Value{}, err
			}
			points[i] = polygonPoint{point[0], point[1]}
		}
		return Value{typ: p.result, polygon: &polygonPayload{points: points}}, nil
	}
	n := int64(12)
	if p.operation == PolygonCirclePoints {
		n, a = a.integer, values[1]
	}
	v, err := circleConversionPolygon(ctx, p.result, a.circle, n)
	// Only the released unary SQL wrapper adds this function context. Operand
	// evaluation/input errors have already returned before entering this kernel.
	if p.operation == PolygonCircle {
		if diagnostic, ok := err.(*sqlstate.Error); ok {
			copy := *diagnostic
			copy.Context = "SQL function \"polygon\" statement 1"
			err = &copy
		}
	}
	return v, err
}

func circleConversionPolygon(ctx context.Context, result *Descriptor, circle circleDatum, n int64) (Value, error) {
	if math.Abs(circle.radius) <= 1e-6 {
		return Value{}, failure("0A000", "cannot convert circle with radius zero to polygon")
	}
	if n < 2 {
		return Value{}, failure("22023", "must request at least 2 points")
	}
	// REL_18_6 uses signed-int byte counts: Point16, POLYGON header40.
	// This is its representability guard, not an additional allocation quota.
	if n > (math.MaxInt32-40)/16 {
		return Value{}, failure("54000", "too many points requested")
	}
	step, err := checkedFloatingBinary(ArithmeticDivide, Float8, 2*math.Pi, float64(n))
	if err != nil {
		return Value{}, err
	}
	points := make([]polygonPoint, int(n))
	for i := range points {
		if err := ctx.Err(); err != nil {
			return Value{}, err
		}
		angle, err := checkedFloatingBinary(ArithmeticMultiply, Float8, step, float64(i))
		if err != nil {
			return Value{}, err
		}
		dx, err := checkedFloatingBinary(ArithmeticMultiply, Float8, circle.radius, math.Cos(angle))
		if err != nil {
			return Value{}, err
		}
		x, err := checkedFloatingBinary(ArithmeticSubtract, Float8, circle.x, dx)
		if err != nil {
			return Value{}, err
		}
		dy, err := checkedFloatingBinary(ArithmeticMultiply, Float8, circle.radius, math.Sin(angle))
		if err != nil {
			return Value{}, err
		}
		y, err := checkedFloatingBinary(ArithmeticAdd, Float8, circle.y, dy)
		if err != nil {
			return Value{}, err
		}
		points[i] = polygonPoint{x, y}
	}
	return Value{typ: result, polygon: &polygonPayload{points: points}}, nil
}
