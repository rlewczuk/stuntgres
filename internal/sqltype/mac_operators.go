package sqltype

import (
	"context"
	"fmt"
)

func macDescriptor(d *Descriptor) bool {
	return d != nil && (d.id == Macaddr || d.id == Macaddr8)
}

func validMACValue(v Value, d *Descriptor) bool {
	return v.typ == d && (v.null || d.id == Macaddr8 || uint64(v.integer)>>48 == 0)
}

// MACBitwise captures one fixed-width, descriptor-owned signature.
// Six-octet results never retain the unused high sixteen bits.
type MACBitwise struct {
	operator    BitwiseOperator
	left, right *Descriptor
}

func BindMACBitwise(operator BitwiseOperator, left, right *Descriptor) (MACBitwise, error) {
	valid := macDescriptor(left)
	switch operator {
	case BitwiseNot:
		valid = valid && right == nil
	case BitwiseAnd, BitwiseOr:
		valid = valid && right == left
	default:
		valid = false
	}
	if !valid {
		return MACBitwise{}, fmt.Errorf("invalid MAC bitwise signature")
	}
	return MACBitwise{operator, left, right}, nil
}

func (p MACBitwise) Unary(ctx context.Context, a Value) (Value, error) {
	if !macDescriptor(p.left) || p.operator != BitwiseNot || p.right != nil || !validMACValue(a, p.left) {
		return Value{}, fmt.Errorf("invalid unary MAC bitwise value")
	}
	if err := ctx.Err(); err != nil {
		return Value{}, err
	}
	if a.null {
		return Null(p.left), nil
	}
	bits := ^uint64(a.integer)
	if p.left.id == Macaddr {
		bits &= 0xffffffffffff
	}
	return Value{typ: p.left, integer: int64(bits)}, nil
}

func (p MACBitwise) Binary(ctx context.Context, a, b Value) (Value, error) {
	if !macDescriptor(p.left) || p.right != p.left || (p.operator != BitwiseAnd && p.operator != BitwiseOr) || !validMACValue(a, p.left) || !validMACValue(b, p.right) {
		return Value{}, fmt.Errorf("invalid binary MAC bitwise values")
	}
	if err := ctx.Err(); err != nil {
		return Value{}, err
	}
	if a.null || b.null {
		return Null(p.left), nil
	}
	bits := uint64(a.integer) & uint64(b.integer)
	if p.operator == BitwiseOr {
		bits = uint64(a.integer) | uint64(b.integer)
	}
	return Value{typ: p.left, integer: int64(bits)}, nil
}

type MACUnaryKind uint8

const (
	MACTrunc MACUnaryKind = iota
	MACSet7Bit
)

// MACUnary retains the original input width. In particular the function-local
// EUI-48 expansion is not a general implicit MACADDR-to-MACADDR8 cast.
type MACUnary struct {
	kind          MACUnaryKind
	input, result *Descriptor
}

func validMACUnarySignature(kind MACUnaryKind, input, result *Descriptor) bool {
	if !macDescriptor(input) || !macDescriptor(result) {
		return false
	}
	switch kind {
	case MACTrunc:
		return input == result
	case MACSet7Bit:
		return result.id == Macaddr8 && (input.id != Macaddr8 || input == result)
	default:
		return false
	}
}

func BindMACUnary(kind MACUnaryKind, input, result *Descriptor) (MACUnary, error) {
	if !validMACUnarySignature(kind, input, result) {
		return MACUnary{}, fmt.Errorf("invalid MAC unary signature")
	}
	return MACUnary{kind, input, result}, nil
}

func (p MACUnary) Evaluate(ctx context.Context, a Value) (Value, error) {
	if !validMACUnarySignature(p.kind, p.input, p.result) || !validMACValue(a, p.input) {
		return Value{}, fmt.Errorf("invalid MAC unary value")
	}
	if err := ctx.Err(); err != nil {
		return Value{}, err
	}
	if a.null {
		return Null(p.result), nil
	}
	bits := uint64(a.integer)
	if p.kind == MACTrunc {
		if p.input.id == Macaddr {
			bits &= 0xffffff000000
		} else {
			bits &= 0xffffff0000000000
		}
	} else {
		if p.input.id == Macaddr {
			bits = (bits&0xffffff000000)<<16 | 0xfffe000000 | (bits & 0xffffff)
		}
		bits |= 0x0200000000000000
	}
	return Value{typ: p.result, integer: int64(bits)}, nil
}
