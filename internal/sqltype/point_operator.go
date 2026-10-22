package sqltype

import (
	"context"
	"fmt"
	"math"
)

// PointOperator enumerates real point predicates, not default equality/order.
type PointOperator uint8

const (
	PointSame PointOperator = iota
	PointNotEqual
	PointLeft
	PointRight
	PointBelow
	PointAbove
	PointHorizontal
	PointVertical
)

var pointOperations = [...]struct{ name, routine string }{
	{"~=", "point_eq"}, {"<>", "point_ne"}, {"<<", "point_left"}, {">>", "point_right"},
	{"<<|", "point_below"}, {"|>>", "point_above"}, {"?-", "point_horiz"}, {"?|", "point_vert"},
}

func PointKind(name string) (PointOperator, bool) {
	switch name {
	case "!=":
		name = "<>"
	case "<^":
		name = "<<|"
	case ">^":
		name = "|>>"
	}
	for i, op := range pointOperations {
		if name == op.name || name == op.routine {
			return PointOperator(i), true
		}
	}
	return 0, false
}

func (o PointOperator) Name() string {
	if int(o) >= len(pointOperations) {
		return ""
	}
	return pointOperations[o].name
}
func (o PointOperator) Routine() string {
	if int(o) >= len(pointOperations) {
		return ""
	}
	return pointOperations[o].routine
}

type PointPlan struct {
	operation     PointOperator
	input, result *Descriptor
}

func BindPoint(op PointOperator, result, left, right Usage) (PointPlan, error) {
	if op.Name() == "" || left.Type == nil || left.Type.id != Point || left != left.Type.Usage() || right != left || result.Type == nil || result.Type.id != Bool || result != result.Type.Usage() {
		return PointPlan{}, fmt.Errorf("invalid point signature")
	}
	if result.Type != left.Type.pointBoolean {
		return PointPlan{}, fmt.Errorf("foreign point result authority")
	}
	return PointPlan{op, left.Type, result.Type}, nil
}

func (p PointPlan) Operation() PointOperator { return p.operation }
func (p PointPlan) Name() string             { return p.operation.Name() }

// A NaN on any axis selects exact PostgreSQL float equality for both axes.
// LSEG endpoint sameness shares this rule, independently for each endpoint.
func pointSame(ax, ay, bx, by float64) bool {
	if math.IsNaN(ax) || math.IsNaN(ay) || math.IsNaN(bx) || math.IsNaN(by) {
		return compareFloats(ax, bx) == 0 && compareFloats(ay, by) == 0
	}
	return boxFuzzyEqual(ax, bx) && boxFuzzyEqual(ay, by)
}

func (p PointPlan) Evaluate(ctx context.Context, values [2]Value) (Value, error) {
	if err := ctx.Err(); err != nil {
		return Value{}, err
	}
	if p.input == nil || p.result == nil || p.operation.Name() == "" {
		return Value{}, fmt.Errorf("invalid point plan")
	}
	for _, v := range values {
		if v.typ != p.input {
			return Value{}, fmt.Errorf("invalid point operand")
		}
	}
	if values[0].null || values[1].null {
		return Null(p.result), nil
	}
	ax, ay := pointCoordinates(values[0])
	bx, by := pointCoordinates(values[1])
	var truth bool
	switch p.operation {
	case PointSame:
		truth = pointSame(ax, ay, bx, by)
	case PointNotEqual:
		truth = !pointSame(ax, ay, bx, by)
	case PointLeft:
		truth = ax+1e-6 < bx
	case PointRight:
		truth = ax > bx+1e-6
	case PointBelow:
		truth = ay+1e-6 < by
	case PointAbove:
		truth = ay > by+1e-6
	case PointHorizontal:
		truth = boxFuzzyEqual(ay, by)
	case PointVertical:
		truth = boxFuzzyEqual(ax, bx)
	}
	return Boolean(p.result, truth), nil
}
