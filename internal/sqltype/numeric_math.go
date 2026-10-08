package sqltype

import (
	"context"
	"math/big"
)

func numericModulo(ctx context.Context, a, b Value) (Value, error) {
	if a.special == numericNaN || b.special == numericNaN {
		return Value{typ: a.typ, special: numericNaN}, nil
	}
	if b.special == numericFinite && b.text == "" {
		return Value{}, failure("22012", "division by zero")
	}
	if a.special != numericFinite {
		return Value{typ: a.typ, special: numericNaN}, nil
	}
	if b.special != numericFinite {
		return a, nil
	}
	scale := max(a.scale, b.scale)
	x, y := a, b
	x.negative = false
	y.negative = false
	order := compareNumeric(x, y)
	if order < 0 {
		a.scale = scale
		return a, nil
	}
	if order == 0 {
		return Value{typ: a.typ, scale: scale}, nil
	}
	exponent := min(a.exponent, b.exponent)
	coefficient := func(v Value) big.Int {
		var n, power big.Int
		n.SetString(v.text, 10)
		if v.exponent != exponent {
			power.Exp(big.NewInt(10), big.NewInt(v.exponent-exponent), nil)
			n.Mul(&n, &power)
		}
		return n
	}
	left := coefficient(a)
	if err := ctx.Err(); err != nil {
		return Value{}, err
	}
	right := coefficient(b)
	if err := ctx.Err(); err != nil {
		return Value{}, err
	}
	left.Rem(&left, &right)
	if err := ctx.Err(); err != nil {
		return Value{}, err
	}
	return normalizeNumeric(Value{typ: a.typ, text: left.String(), exponent: exponent, scale: scale, negative: a.negative}), nil
}

func numericCeilFloor(ctx context.Context, a Value, ceiling bool) (Value, error) {
	if a.special != numericFinite {
		return a, nil
	}
	fractional := a.text != "" && a.exponent < 0
	result := truncateNumeric(a, 0)
	if !fractional || ceiling == a.negative {
		return result, nil
	}
	if err := ctx.Err(); err != nil {
		return Value{}, err
	}
	// Work on the unsigned integer magnitude: ceil(positive) and
	// floor(negative) each increase that magnitude by exactly one.
	var n, power, one big.Int
	if result.text != "" {
		n.SetString(result.text, 10)
		if result.exponent > 0 {
			power.Exp(big.NewInt(10), big.NewInt(result.exponent), nil)
			n.Mul(&n, &power)
		}
	}
	one.SetInt64(1)
	n.Add(&n, &one)
	if err := ctx.Err(); err != nil {
		return Value{}, err
	}
	digits := n.String()
	if len(digits) > 131072 {
		return Value{}, failure("22003", "value overflows numeric format")
	}
	return normalizeNumeric(Value{typ: a.typ, text: digits, negative: a.negative}), nil
}

func numericDivideTrunc(ctx context.Context, a, b Value) (Value, error) {
	if a.special == numericNaN || b.special == numericNaN {
		return Value{typ: a.typ, special: numericNaN}, nil
	}
	if b.special == numericFinite && b.text == "" {
		return Value{}, failure("22012", "division by zero")
	}
	if a.special != numericFinite {
		if b.special != numericFinite {
			return Value{typ: a.typ, special: numericNaN}, nil
		}
		special := uint8(numericInfinity)
		if (a.special == numericNegativeInfinity) != b.negative {
			special = numericNegativeInfinity
		}
		return Value{typ: a.typ, special: special}, nil
	}
	if b.special != numericFinite || a.text == "" {
		return Value{typ: a.typ}, nil
	}
	x, y := a, b
	x.negative, y.negative = false, false
	if compareNumeric(x, y) < 0 {
		return Value{typ: a.typ}, nil
	}
	// Magnitude difference bounds the quotient's decimal weight to within
	// one digit. Reject definite overflow before expanding virtual zeros;
	// the borderline case still needs exact division.
	weight := int64(len(a.text)) + a.exponent - int64(len(b.text)) - b.exponent
	if weight > 131072 {
		return Value{}, failure("22003", "value overflows numeric format")
	}
	if err := ctx.Err(); err != nil {
		return Value{}, err
	}
	var left, right, power big.Int
	left.SetString(a.text, 10)
	right.SetString(b.text, 10)
	if err := ctx.Err(); err != nil {
		return Value{}, err
	}
	difference := a.exponent - b.exponent
	if difference != 0 {
		exponent := difference
		if exponent < 0 {
			exponent = -exponent
		}
		power.Exp(big.NewInt(10), big.NewInt(exponent), nil)
		if difference > 0 {
			left.Mul(&left, &power)
		} else {
			right.Mul(&right, &power)
		}
	}
	if err := ctx.Err(); err != nil {
		return Value{}, err
	}
	left.Quo(&left, &right)
	if err := ctx.Err(); err != nil {
		return Value{}, err
	}
	digits := left.String()
	if len(digits) > 131072 {
		return Value{}, failure("22003", "value overflows numeric format")
	}
	return normalizeNumeric(Value{typ: a.typ, text: digits, negative: a.negative != b.negative}), nil
}
