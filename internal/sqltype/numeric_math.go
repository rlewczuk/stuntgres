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
