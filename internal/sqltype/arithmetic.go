package sqltype

import (
	"context"
	"fmt"
	"math"
)

type ArithmeticOperator uint8

const (
	ArithmeticAdd ArithmeticOperator = iota
	ArithmeticSubtract
	ArithmeticMultiply
	ArithmeticDivide
	ArithmeticRemainder
	ArithmeticPower
)

// Arithmetic binds actual input widths independently from the result width.
type Arithmetic struct {
	operator            ArithmeticOperator
	left, right, result *Descriptor
}

func BindArithmetic(operator ArithmeticOperator, left, right *Descriptor) (Arithmetic, error) {
	if left == nil || operator > ArithmeticPower || right == nil && operator > ArithmeticSubtract {
		return Arithmetic{}, fmt.Errorf("invalid arithmetic signature")
	}
	result := left
	if isFloat(left.id) {
		if right != nil {
			if !isFloat(right.id) || operator == ArithmeticRemainder || operator == ArithmeticPower && (left.id != Float8 || right.id != Float8) {
				return Arithmetic{}, fmt.Errorf("invalid floating arithmetic signature")
			}
			if right.id == Float8 {
				result = right
			}
		}
	} else if (left.id != Int4 && left.id != Int8) || right != nil && (right != left || operator == ArithmeticPower) {
		return Arithmetic{}, fmt.Errorf("invalid integer arithmetic signature")
	}
	return Arithmetic{operator, left, right, result}, nil
}

func (p Arithmetic) ResultType() *Descriptor { return p.result }
func (p Arithmetic) Unary(ctx context.Context, a Value) (Value, error) {
	if p.right != nil || p.left == nil || a.typ != p.left {
		return Value{}, fmt.Errorf("invalid unary arithmetic values")
	}
	if err := ctx.Err(); err != nil {
		return Value{}, err
	}
	if a.null {
		return Null(p.result), nil
	}
	if isFloat(p.left.id) {
		n := a.floating
		if p.operator == ArithmeticSubtract {
			n = -n
		}
		return Value{typ: p.result, floating: n}, nil
	}
	n := a.integer
	if p.operator == ArithmeticSubtract {
		if n == math.MinInt64 {
			return Value{}, failure("22003", "bigint out of range")
		}
		n = -n
	}
	return Integer(p.result, n)
}
func (p Arithmetic) Binary(ctx context.Context, a, b Value) (Value, error) {
	if p.right == nil || p.left == nil || a.typ != p.left || b.typ != p.right {
		return Value{}, fmt.Errorf("invalid binary arithmetic values")
	}
	if err := ctx.Err(); err != nil {
		return Value{}, err
	}
	if a.null || b.null {
		return Null(p.result), nil
	}
	if isFloat(p.result.id) {
		return p.floatingBinary(a.floating, b.floating)
	}
	x, y := a.integer, b.integer
	var n int64
	overflow := false
	switch p.operator {
	case ArithmeticAdd:
		overflow = (y > 0 && x > math.MaxInt64-y) || (y < 0 && x < math.MinInt64-y)
		if !overflow {
			n = x + y
		}
	case ArithmeticSubtract:
		overflow = (y < 0 && x > math.MaxInt64+y) || (y > 0 && x < math.MinInt64+y)
		if !overflow {
			n = x - y
		}
	case ArithmeticMultiply:
		if x != 0 && y != 0 {
			if x == -1 {
				overflow = y == math.MinInt64
			} else if y == -1 {
				overflow = x == math.MinInt64
			} else if x > 0 {
				if y > 0 {
					overflow = x > math.MaxInt64/y
				} else {
					overflow = y < math.MinInt64/x
				}
			} else {
				if y > 0 {
					overflow = x < math.MinInt64/y
				} else {
					overflow = x < math.MaxInt64/y
				}
			}
		}
		if !overflow {
			n = x * y
		}
	case ArithmeticDivide, ArithmeticRemainder:
		if y == 0 {
			return Value{}, failure("22012", "division by zero")
		}
		if p.operator == ArithmeticRemainder {
			if y != -1 {
				n = x % y
			}
		} else {
			overflow = x == math.MinInt64 && y == -1
			if !overflow {
				n = x / y
			}
		}
	}
	if overflow {
		message := "bigint out of range"
		if p.result.id == Int4 {
			message = "integer out of range"
		}
		return Value{}, failure("22003", message)
	}
	return Integer(p.result, n)
}
