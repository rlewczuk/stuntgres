package sqltype

import (
	"context"
	"fmt"
	"math"
)

// CircleOperator identifies actual scalar signatures, not default comparison
// capabilities. Area predicates are deliberately not a three-way comparator.
type CircleOperator uint8

const (
	CircleEqual CircleOperator = iota
	CircleNotEqual
	CircleLess
	CircleGreater
	CircleLessEqual
	CircleGreaterEqual
	CircleSame
	CircleLeft
	CircleRight
	CircleOverleft
	CircleOverright
	CircleBelow
	CircleAbove
	CircleOverbelow
	CircleOverabove
	CircleContains
	CircleContained
	CircleOverlap
	CircleDistance
	CircleRadius
)

var circleOperations = [...]struct{ name, routine string }{
	{"=", "circle_eq"}, {"<>", "circle_ne"}, {"<", "circle_lt"}, {">", "circle_gt"}, {"<=", "circle_le"}, {">=", "circle_ge"},
	{"~=", "circle_same"}, {"<<", "circle_left"}, {">>", "circle_right"}, {"&<", "circle_overleft"}, {"&>", "circle_overright"},
	{"<<|", "circle_below"}, {"|>>", "circle_above"}, {"&<|", "circle_overbelow"}, {"|&>", "circle_overabove"},
	{"@>", "circle_contain"}, {"<@", "circle_contained"}, {"&&", "circle_overlap"}, {"<->", "circle_distance"}, {"radius", "radius"},
}

func CircleKind(name string) (CircleOperator, bool) {
	if name == "!=" {
		name = "<>"
	}
	for i, op := range circleOperations {
		if name == op.name || name == op.routine {
			return CircleOperator(i), true
		}
	}
	return 0, false
}
func (o CircleOperator) Name() string {
	if int(o) >= len(circleOperations) {
		return ""
	}
	return circleOperations[o].name
}
func (o CircleOperator) Routine() string {
	if int(o) >= len(circleOperations) {
		return ""
	}
	return circleOperations[o].routine
}
func (o CircleOperator) Arity() int {
	if o == CircleRadius {
		return 1
	}
	return 2
}
func (o CircleOperator) ResultID() ID {
	if o == CircleRadius || o == CircleDistance {
		return Float8
	}
	return Bool
}

type CirclePlan struct {
	operation     CircleOperator
	input, result *Descriptor
}

func BindCircle(op CircleOperator, result, left, right Usage) (CirclePlan, error) {
	if op.Name() == "" || left.Type == nil || left.Type.id != Circle || left != left.Type.Usage() || op.Arity() == 2 && right != left || result.Type == nil || result.Type.id != op.ResultID() || result != result.Type.Usage() {
		return CirclePlan{}, fmt.Errorf("invalid circle signature")
	}
	expected := left.Type.circleBoolean
	if op.ResultID() == Float8 {
		expected = left.Type.circleFloat
	}
	if result.Type != expected {
		return CirclePlan{}, fmt.Errorf("foreign circle result authority")
	}
	return CirclePlan{op, left.Type, result.Type}, nil
}
func (p CirclePlan) Operation() CircleOperator { return p.operation }
func (p CirclePlan) Name() string              { return p.operation.Name() }
func (p CirclePlan) Evaluate(ctx context.Context, values [2]Value) (Value, error) {
	if err := ctx.Err(); err != nil {
		return Value{}, err
	}
	if p.input == nil || p.result == nil || p.operation.Name() == "" {
		return Value{}, fmt.Errorf("invalid circle plan")
	}
	for _, v := range values[:p.operation.Arity()] {
		if v.typ != p.input {
			return Value{}, fmt.Errorf("invalid circle operand")
		}
	}
	for _, v := range values[:p.operation.Arity()] {
		if v.null {
			return Null(p.result), nil
		}
		if v.circle.radius < 0 {
			return Value{}, fmt.Errorf("invalid circle datum")
		}
	}
	a, b := values[0].circle, values[1].circle
	if p.operation == CircleRadius {
		return Float(p.result, a.radius)
	}
	if p.operation == CircleSame {
		center := circleFPeq(a.x, b.x) && circleFPeq(a.y, b.y)
		if math.IsNaN(a.x) || math.IsNaN(a.y) || math.IsNaN(b.x) || math.IsNaN(b.y) {
			center = compareFloats(a.x, b.x) == 0 && compareFloats(a.y, b.y) == 0
		}
		return Boolean(p.result, center && (circleFPeq(a.radius, b.radius) || math.IsNaN(a.radius) && math.IsNaN(b.radius))), nil
	}
	var x, y float64
	var err error
	op := p.operation
	switch {
	case op <= CircleGreaterEqual:
		x, err = circleArea(a.radius)
		if err == nil {
			y, err = circleArea(b.radius)
		}
	case op >= CircleLeft && op <= CircleOverabove:
		ax, bx := a.x, b.x
		if op >= CircleBelow {
			ax, bx = a.y, b.y
		}
		first, second := ArithmeticAdd, ArithmeticAdd
		switch op {
		case CircleLeft, CircleBelow:
			second = ArithmeticSubtract
		case CircleRight, CircleAbove:
			first = ArithmeticSubtract
		case CircleOverright, CircleOverabove:
			first, second = ArithmeticSubtract, ArithmeticSubtract
		}
		x, err = checkedFloatingBinary(first, Float8, ax, a.radius)
		if err == nil {
			y, err = checkedFloatingBinary(second, Float8, bx, b.radius)
		}
	default:
		x, err = circleCenterDistance(a, b)
		if err == nil {
			switch op {
			case CircleContains:
				y, err = checkedFloatingBinary(ArithmeticSubtract, Float8, a.radius, b.radius)
			case CircleContained:
				y, err = checkedFloatingBinary(ArithmeticSubtract, Float8, b.radius, a.radius)
			default:
				y, err = checkedFloatingBinary(ArithmeticAdd, Float8, a.radius, b.radius)
			}
		}
	}
	if err != nil {
		return Value{}, err
	}
	if op == CircleDistance {
		n, err := checkedFloatingBinary(ArithmeticSubtract, Float8, x, y)
		if err != nil {
			return Value{}, err
		}
		if n < 0 {
			n = 0
		}
		return Float(p.result, n)
	}
	var match bool
	switch op {
	case CircleEqual:
		match = circleFPeq(x, y)
	case CircleNotEqual:
		match = x != y && math.Abs(x-y) > 1e-6
	case CircleLess, CircleLeft, CircleBelow:
		match = x+1e-6 < y
	case CircleGreater, CircleRight, CircleAbove:
		match = x > y+1e-6
	case CircleGreaterEqual, CircleOverright, CircleOverabove:
		match = x+1e-6 >= y
	default:
		match = x <= y+1e-6
	}
	return Boolean(p.result, match), nil
}
func circleFPeq(x, y float64) bool { return x == y || math.Abs(x-y) <= 1e-6 }
func circleArea(radius float64) (float64, error) {
	n, err := checkedFloatingBinary(ArithmeticMultiply, Float8, radius, radius)
	if err != nil {
		return 0, err
	}
	return checkedFloatingBinary(ArithmeticMultiply, Float8, n, math.Pi)
}
func circleCenterDistance(a, b circleDatum) (float64, error) {
	x, err := checkedFloatingBinary(ArithmeticSubtract, Float8, a.x, b.x)
	if err != nil {
		return 0, err
	}
	y, err := checkedFloatingBinary(ArithmeticSubtract, Float8, a.y, b.y)
	if err != nil {
		return 0, err
	}
	// REL_18_6 pg_hypot: infinity wins over NaN; scale by the larger
	// coordinate, retaining its operation order rather than squaring directly.
	if math.IsInf(x, 0) || math.IsInf(y, 0) {
		return math.Inf(1), nil
	}
	if math.IsNaN(x) || math.IsNaN(y) {
		return math.NaN(), nil
	}
	x, y = math.Abs(x), math.Abs(y)
	if x < y {
		x, y = y, x
	}
	if y == 0 {
		return x, nil
	}
	ratio := y / x
	n := x * math.Sqrt(1+ratio*ratio)
	if math.IsInf(n, 0) {
		return 0, failure("22003", "value out of range: overflow")
	}
	if n == 0 {
		return 0, failure("22003", "value out of range: underflow")
	}
	return n, nil
}
