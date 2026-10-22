package sqltype

import (
	"context"
	"fmt"
)

type PolygonOperator uint8

const (
	PolygonSame PolygonOperator = iota
	PolygonContainsPoint
	PointContainedPolygon
)

var polygonOperations = [...]struct {
	name, routine string
	left, right   ID
}{
	{"~=", "poly_same", Polygon, Polygon},
	{"@>", "poly_contain_pt", Polygon, Point},
	{"<@", "pt_contained_poly", Point, Polygon},
}

func PolygonKind(name string) (PolygonOperator, bool) {
	for i, op := range polygonOperations {
		if name == op.name || name == op.routine {
			return PolygonOperator(i), true
		}
	}
	return 0, false
}
func (o PolygonOperator) Name() string {
	if int(o) >= len(polygonOperations) {
		return ""
	}
	return polygonOperations[o].name
}
func (o PolygonOperator) Routine() string {
	if int(o) >= len(polygonOperations) {
		return ""
	}
	return polygonOperations[o].routine
}
func (o PolygonOperator) Signature() (ID, ID) {
	if o.Name() == "" {
		return 0, 0
	}
	s := polygonOperations[o]
	return s.left, s.right
}

type PolygonPlan struct {
	operation           PolygonOperator
	left, right, result *Descriptor
}

func BindPolygon(op PolygonOperator, result, left, right Usage) (PolygonPlan, error) {
	l, r := op.Signature()
	if l == 0 || left.Type == nil || right.Type == nil || result.Type == nil || left.Type.id != l || right.Type.id != r || result.Type.id != Bool || left != left.Type.Usage() || right != right.Type.Usage() || result != result.Type.Usage() {
		return PolygonPlan{}, fmt.Errorf("invalid polygon signature")
	}
	polygon := left.Type
	if l == Point {
		polygon = right.Type
	}
	if polygon.polygonBoolean != result.Type || l == Point && left.Type != polygon.polygonPoint || r == Point && right.Type != polygon.polygonPoint || l == Polygon && r == Polygon && left.Type != right.Type {
		return PolygonPlan{}, fmt.Errorf("foreign polygon authority")
	}
	return PolygonPlan{op, left.Type, right.Type, result.Type}, nil
}
func (p PolygonPlan) Operation() PolygonOperator { return p.operation }
func (p PolygonPlan) Name() string               { return p.operation.Name() }
func (p PolygonPlan) Evaluate(ctx context.Context, values [2]Value) (Value, error) {
	if err := ctx.Err(); err != nil {
		return Value{}, err
	}
	if p.result == nil || p.operation.Name() == "" || values[0].typ != p.left || values[1].typ != p.right {
		return Value{}, fmt.Errorf("invalid polygon operands")
	}
	for _, v := range values {
		if !v.null && v.typ.id == Polygon && !v.validPolygon() {
			return Value{}, fmt.Errorf("missing polygon payload")
		}
	}
	if values[0].null || values[1].null {
		return Null(p.result), nil
	}
	var match bool
	var err error
	if p.operation == PolygonSame {
		match, err = polygonSame(ctx, values[0].polygon.points, values[1].polygon.points)
	} else {
		polygon, point := values[0], values[1]
		if p.operation == PointContainedPolygon {
			polygon, point = point, polygon
		}
		x, y := pointCoordinates(point)
		match, err = polygonContains(ctx, polygon.polygon.points, x, y)
	}
	if err != nil {
		return Value{}, err
	}
	return Boolean(p.result, match), nil
}
func polygonSame(ctx context.Context, a, b []polygonPoint) (bool, error) {
	if len(a) != len(b) {
		return false, nil
	}
	equal := func(p, q polygonPoint) bool { return pointSame(p.x, p.y, q.x, q.y) }
	for i, candidate := range b {
		if err := ctx.Err(); err != nil {
			return false, err
		}
		if !equal(a[0], candidate) {
			continue
		}
		for _, direction := range [2]int{1, -1} {
			j, matched := i, true
			for k := 1; k < len(a); k++ {
				if err := ctx.Err(); err != nil {
					return false, err
				}
				j += direction
				if j == len(b) {
					j = 0
				} else if j < 0 {
					j = len(b) - 1
				}
				if !equal(a[k], b[j]) {
					matched = false
					break
				}
			}
			if matched {
				return true, nil
			}
		}
	}
	return false, nil
}

// REL_18_6 point_inside/lseg_crossing: boundary counts as contained. Keep the
// released epsilon branches and checked arithmetic order, including nonfinite
// inputs, rather than substituting a bounding box or convex-only algorithm.
func polygonContains(ctx context.Context, points []polygonPoint, px, py float64) (bool, error) {
	relative := func(p polygonPoint) (float64, float64, error) {
		x, err := checkedFloatingBinary(ArithmeticSubtract, Float8, p.x, px)
		if err != nil {
			return 0, 0, err
		}
		y, err := checkedFloatingBinary(ArithmeticSubtract, Float8, p.y, py)
		return x, y, err
	}
	x0, y0, err := relative(points[0])
	if err != nil {
		return false, err
	}
	prevX, prevY := x0, y0
	var total int64
	for i := 1; i < len(points); i++ {
		if err := ctx.Err(); err != nil {
			return false, err
		}
		x, y, err := relative(points[i])
		if err != nil {
			return false, err
		}
		cross, boundary, err := polygonCrossing(x, y, prevX, prevY)
		if err != nil || boundary {
			return boundary, err
		}
		total += int64(cross)
		prevX, prevY = x, y
	}
	cross, boundary, err := polygonCrossing(x0, y0, prevX, prevY)
	return boundary || total+int64(cross) != 0, err
}
func polygonCrossing(x, y, prevX, prevY float64) (int, bool, error) {
	zero := func(v float64) bool { return circleFPeq(v, 0) }
	lt := func(v float64) bool { return v+1e-6 < 0 }
	gt := func(v float64) bool { return v > 1e-6 }
	if zero(y) {
		if zero(x) {
			return 0, true, nil
		}
		if gt(x) {
			if zero(prevY) {
				return 0, !gt(prevX), nil
			}
			if lt(prevY) {
				return 1, false, nil
			}
			return -1, false, nil
		}
		if zero(prevY) {
			return 0, !lt(prevX), nil
		}
		return 0, false, nil
	}
	sign := 1
	if !gt(y) {
		sign = -1
	}
	if zero(prevY) {
		if lt(prevX) {
			return 0, false, nil
		}
		return sign, false, nil
	}
	if sign < 0 && lt(prevY) || sign > 0 && gt(prevY) {
		return 0, false, nil
	}
	if x+1e-6 >= 0 && gt(prevX) {
		return 2 * sign, false, nil
	}
	if lt(x) && prevX <= 1e-6 {
		return 0, false, nil
	}
	dx, err := checkedFloatingBinary(ArithmeticSubtract, Float8, x, prevX)
	if err != nil {
		return 0, false, err
	}
	a, err := checkedFloatingBinary(ArithmeticMultiply, Float8, dx, y)
	if err != nil {
		return 0, false, err
	}
	dy, err := checkedFloatingBinary(ArithmeticSubtract, Float8, y, prevY)
	if err != nil {
		return 0, false, err
	}
	b, err := checkedFloatingBinary(ArithmeticMultiply, Float8, dy, x)
	if err != nil {
		return 0, false, err
	}
	z, err := checkedFloatingBinary(ArithmeticSubtract, Float8, a, b)
	if err != nil {
		return 0, false, err
	}
	if zero(z) {
		return 0, true, nil
	}
	if sign < 0 && lt(z) || sign > 0 && gt(z) {
		return 0, false, nil
	}
	return 2 * sign, false, nil
}
