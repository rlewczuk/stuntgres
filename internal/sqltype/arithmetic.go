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
)

// Arithmetic is an immutable, concrete-width integer provider.
type Arithmetic struct {
	operator ArithmeticOperator
	operand  *Descriptor
	unary    bool
}

func BindArithmetic(operator ArithmeticOperator, operand *Descriptor, unary bool) (Arithmetic, error) {
	if operand == nil || (operand.id != Int4 && operand.id != Int8) || operator > ArithmeticRemainder || (unary && operator > ArithmeticSubtract) {
		return Arithmetic{}, fmt.Errorf("invalid integer arithmetic signature")
	}
	return Arithmetic{operator, operand, unary}, nil
}
func (p Arithmetic) Unary(ctx context.Context, a Value) (Value, error) {
	if !p.unary || p.operand == nil || a.typ != p.operand {
		return Value{}, fmt.Errorf("invalid unary arithmetic values")
	}
	if err := ctx.Err(); err != nil {
		return Value{}, err
	}
	if a.null {
		return Null(p.operand), nil
	}
	n := a.integer
	if p.operator == ArithmeticSubtract {
		if n == math.MinInt64 {
			return Value{}, failure("22003", "bigint out of range")
		}
		n = -n
	}
	return Integer(p.operand, n)
}
func (p Arithmetic) Binary(ctx context.Context, a, b Value) (Value, error) {
	if p.unary || p.operand == nil || a.typ != p.operand || b.typ != p.operand {
		return Value{}, fmt.Errorf("invalid binary arithmetic values")
	}
	if err := ctx.Err(); err != nil {
		return Value{}, err
	}
	if a.null || b.null {
		return Null(p.operand), nil
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
		if p.operand.id == Int4 {
			message = "integer out of range"
		}
		return Value{}, failure("22003", message)
	}
	return Integer(p.operand, n)
}
