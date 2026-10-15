package sqltype

import (
	"context"
	"fmt"
	"strings"
)

// BitStringBitwise owns a selected BIT signature. Canonical digits never pass
// through the integral provider, so payload widths and shift counts stay exact.
type BitStringBitwise struct {
	operator    BitwiseOperator
	left, right *Descriptor
}

func BindBitStringBitwise(operator BitwiseOperator, left, right *Descriptor) (BitStringBitwise, error) {
	valid := left != nil && left.id == Bit
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
		return BitStringBitwise{}, fmt.Errorf("invalid bit-string bitwise signature")
	}
	return BitStringBitwise{operator, left, right}, nil
}

func (p BitStringBitwise) Unary(ctx context.Context, a Value) (Value, error) {
	if p.left == nil || p.operator != BitwiseNot || p.right != nil || a.typ != p.left {
		return Value{}, fmt.Errorf("invalid unary bit-string values")
	}
	if err := ctx.Err(); err != nil {
		return Value{}, err
	}
	if a.null {
		return Null(p.left), nil
	}
	var out strings.Builder
	out.Grow(len(a.text))
	for i := range len(a.text) {
		if i&4095 == 0 {
			if err := ctx.Err(); err != nil {
				return Value{}, err
			}
		}
		out.WriteByte(a.text[i] ^ 1)
	}
	if err := ctx.Err(); err != nil {
		return Value{}, err
	}
	return Value{typ: p.left, text: out.String()}, nil
}

func (p BitStringBitwise) Binary(ctx context.Context, a, b Value) (Value, error) {
	if p.left == nil || p.right == nil || p.operator == BitwiseNot || p.operator > BitwiseShiftRight || a.typ != p.left || b.typ != p.right {
		return Value{}, fmt.Errorf("invalid binary bit-string values")
	}
	if err := ctx.Err(); err != nil {
		return Value{}, err
	}
	if a.null || b.null {
		return Null(p.left), nil
	}
	if p.operator == BitwiseShiftLeft || p.operator == BitwiseShiftRight {
		return p.shift(ctx, a, b.integer)
	}
	if len(a.text) != len(b.text) {
		name := [...]string{"AND", "OR", "XOR"}[p.operator]
		return Value{}, failure("22026", "cannot "+name+" bit strings of different sizes")
	}
	var out strings.Builder
	out.Grow(len(a.text))
	for i := range len(a.text) {
		if i&4095 == 0 {
			if err := ctx.Err(); err != nil {
				return Value{}, err
			}
		}
		var digit byte
		switch p.operator {
		case BitwiseAnd:
			digit = a.text[i] & b.text[i]
		case BitwiseOr:
			digit = a.text[i] | b.text[i]
		case BitwiseXor:
			digit = '0' + (a.text[i] ^ b.text[i])
		}
		out.WriteByte(digit)
	}
	if err := ctx.Err(); err != nil {
		return Value{}, err
	}
	return Value{typ: p.left, text: out.String()}, nil
}

func (p BitStringBitwise) shift(ctx context.Context, a Value, count int64) (Value, error) {
	if count == 0 || len(a.text) == 0 {
		return a, nil
	}
	left := p.operator == BitwiseShiftLeft
	if count < 0 {
		// INT4 counts are held in int64; negating INT32_MIN cannot overflow.
		count = -count
		left = !left
	}
	n := int(min(count, int64(len(a.text))))
	var out strings.Builder
	out.Grow(len(a.text))
	if left {
		if err := stringWrite(ctx, &out, a.text[n:]); err != nil {
			return Value{}, err
		}
	}
	for i := range n {
		if i&4095 == 0 {
			if err := ctx.Err(); err != nil {
				return Value{}, err
			}
		}
		out.WriteByte('0')
	}
	if !left {
		if err := stringWrite(ctx, &out, a.text[:len(a.text)-n]); err != nil {
			return Value{}, err
		}
	}
	if err := ctx.Err(); err != nil {
		return Value{}, err
	}
	return Value{typ: p.left, text: out.String()}, nil
}
