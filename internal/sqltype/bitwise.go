package sqltype

import (
	"context"
	"fmt"
)

type BitwiseOperator uint8

const (
	BitwiseAnd BitwiseOperator = iota
	BitwiseOr
	BitwiseXor
	BitwiseNot
	BitwiseShiftLeft
	BitwiseShiftRight
)

// Bitwise is an immutable integral signature, separate from bit strings.
// Shift counts match the pinned PostgreSQL 18 x86_64 target: modulo 32 for
// SMALLINT/INTEGER (SMALLINT is promoted), and modulo 64 for BIGINT.
type Bitwise struct {
	operator    BitwiseOperator
	left, right *Descriptor
	// Descriptor pointers retain ownership independently for each operand.
	// Catalog validation, not Descriptor, owns the generation relationship.
}

func BindBitwise(operator BitwiseOperator, left, right *Descriptor) (Bitwise, error) {
	valid := left != nil && (left.id == Int2 || left.id == Int4 || left.id == Int8)
	switch operator {
	case BitwiseNot:
		valid = valid && right == nil
	case BitwiseAnd, BitwiseOr, BitwiseXor:
		valid = valid && right == left
	case BitwiseShiftLeft, BitwiseShiftRight:
		valid = valid && right != nil && right.id == Int4
	default:
		valid = false
	}
	if !valid {
		return Bitwise{}, fmt.Errorf("invalid integral bitwise signature")
	}
	return Bitwise{operator, left, right}, nil
}

func (p Bitwise) Unary(ctx context.Context, a Value) (Value, error) {
	if p.left == nil || p.operator != BitwiseNot || p.right != nil || a.typ != p.left {
		return Value{}, fmt.Errorf("invalid unary bitwise values")
	}
	if err := ctx.Err(); err != nil {
		return Value{}, err
	}
	if a.null {
		return Null(p.left), nil
	}
	return p.result(^a.integer)
}

func (p Bitwise) Binary(ctx context.Context, a, b Value) (Value, error) {
	if p.left == nil || p.right == nil || p.operator == BitwiseNot || p.operator > BitwiseShiftRight || a.typ != p.left || b.typ != p.right {
		return Value{}, fmt.Errorf("invalid binary bitwise values")
	}
	if err := ctx.Err(); err != nil {
		return Value{}, err
	}
	if a.null || b.null {
		return Null(p.left), nil
	}
	x, y := a.integer, b.integer
	var n int64
	switch p.operator {
	case BitwiseAnd:
		n = x & y
	case BitwiseOr:
		n = x | y
	case BitwiseXor:
		n = x ^ y
	case BitwiseShiftLeft, BitwiseShiftRight:
		mask := uint64(31)
		if p.left.id == Int8 {
			mask = 63
		}
		count := uint64(y) & mask
		if p.operator == BitwiseShiftLeft {
			n = x << count
		} else {
			n = x >> count
		}
	}
	return p.result(n)
}

func (p Bitwise) result(n int64) (Value, error) {
	switch p.left.id {
	case Int2:
		n = int64(int16(n))
	case Int4:
		n = int64(int32(n))
	}
	return Integer(p.left, n)
}
