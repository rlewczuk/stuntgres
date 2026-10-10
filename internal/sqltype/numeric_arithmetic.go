package sqltype

import (
	"context"
)

func numericNegate(a Value) Value {
	switch a.special {
	case numericInfinity:
		a.special = numericNegativeInfinity
	case numericNegativeInfinity:
		a.special = numericInfinity
	case numericFinite:
		if a.text != "" {
			a.negative = !a.negative
		}
	}
	return a
}

func numericArithmetic(ctx context.Context, operator ArithmeticOperator, a, b Value) (Value, error) {
	switch operator {
	case ArithmeticAdd, ArithmeticSubtract:
		if operator == ArithmeticSubtract {
			b = numericNegate(b)
		}
		return numericAdd(ctx, a, b)
	case ArithmeticMultiply:
		return numericMultiply(ctx, a, b)
	case ArithmeticDivide:
		return numericDivide(ctx, a, b)
	case ArithmeticRemainder:
		return numericModulo(ctx, a, b)
	case ArithmeticPower:
		return numericPower(ctx, a, b)
	}
	panic("invalid bound numeric arithmetic operator")
}

func numericAdd(ctx context.Context, a, b Value) (Value, error) {
	if a.special == numericNaN || b.special == numericNaN {
		return Value{typ: a.typ, special: numericNaN}, nil
	}
	if a.special != numericFinite || b.special != numericFinite {
		if a.special != numericFinite && b.special != numericFinite && a.special != b.special {
			return Value{typ: a.typ, special: numericNaN}, nil
		}
		if a.special != numericFinite {
			return Value{typ: a.typ, special: a.special}, nil
		}
		return Value{typ: a.typ, special: b.special}, nil
	}
	scale := max(a.scale, b.scale)
	if a.text == "" {
		b.scale = scale
		return b, nil
	}
	if b.text == "" {
		a.scale = scale
		return a, nil
	}
	var x, y expDecimal
	x.setValue(a)
	y.setValue(b)
	var w expScratch
	w.add(&x, &y)
	return numericDecimalResult(ctx, a.typ, &x, scale)
}

func numericMultiply(ctx context.Context, a, b Value) (Value, error) {
	if a.special == numericNaN || b.special == numericNaN {
		return Value{typ: a.typ, special: numericNaN}, nil
	}
	if a.special != numericFinite || b.special != numericFinite {
		if a.special == numericFinite && a.text == "" || b.special == numericFinite && b.text == "" {
			return Value{typ: a.typ, special: numericNaN}, nil
		}
		special := numericInfinity
		if numericSign(a)*numericSign(b) < 0 {
			special = numericNegativeInfinity
		}
		return Value{typ: a.typ, special: special}, nil
	}
	scale := min(int64(16383), a.scale+b.scale)
	if a.text == "" || b.text == "" {
		return Value{typ: a.typ, scale: scale}, nil
	}
	var x, y, result expDecimal
	x.setValue(a)
	y.setValue(b)
	var w expScratch
	w.multiply(&result, &x, &y, scale)
	return numericDecimalResult(ctx, a.typ, &result, scale)
}

func numericDivide(ctx context.Context, a, b Value) (Value, error) {
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
		special := numericInfinity
		if numericSign(a)*numericSign(b) < 0 {
			special = numericNegativeInfinity
		}
		return Value{typ: a.typ, special: special}, nil
	}
	if b.special != numericFinite {
		return Value{typ: a.typ}, nil
	}
	scale := numericDivisionScale(a.text, a.exponent, a.scale, b.text, b.exponent, b.scale)
	if a.text == "" {
		return Value{typ: a.typ, scale: scale}, nil
	}
	var x, y, result expDecimal
	x.setValue(a)
	y.setValue(b)
	var w expScratch
	w.divide(&result, &x, &y, scale)
	return numericDecimalResult(ctx, a.typ, &result, scale)
}

// Shared with AVG: normalized base10000 weight and leading digit, without
// padded output or a second division precision convention.
func numericLeading(digits string, exponent int64) (int64, int64) {
	if digits == "" {
		return 0, 0
	}
	position := int64(len(digits)) + exponent
	weight := (position - 1) / 4
	if position-1 < 0 && (position-1)%4 != 0 {
		weight--
	}
	width := position - 4*weight
	first := int64(0)
	for i := int64(0); i < width; i++ {
		first *= 10
		if i < int64(len(digits)) {
			first += int64(digits[i] - '0')
		}
	}
	return weight, first
}

func numericDivisionScale(a string, ae, as int64, b string, be, bs int64) int64 {
	aw, af := numericLeading(a, ae)
	bw, bf := numericLeading(b, be)
	weight := aw - bw
	if af <= bf {
		weight--
	}
	return min(int64(1000), max(int64(0), as, bs, 16-4*weight))
}

func numericSign(v Value) int {
	switch v.special {
	case numericNegativeInfinity:
		return -1
	case numericInfinity:
		return 1
	}
	if v.text == "" {
		return 0
	}
	if v.negative {
		return -1
	}
	return 1
}

func (x *expDecimal) setValue(v Value) {
	if v.text == "" {
		x.coefficient.SetInt64(0)
	} else {
		x.coefficient.SetString(v.text, 10)
	}
	if v.negative {
		x.coefficient.Neg(&x.coefficient)
	}
	x.exponent = v.exponent
}

func numericDecimalResult(ctx context.Context, d *Descriptor, x *expDecimal, scale int64) (Value, error) {
	if err := ctx.Err(); err != nil {
		return Value{}, err
	}
	negative := x.coefficient.Sign() < 0
	x.coefficient.Abs(&x.coefficient)
	v := Value{typ: d, exponent: x.exponent, scale: scale, negative: negative}
	if x.coefficient.Sign() != 0 {
		v.text = x.coefficient.String()
	}
	v = normalizeNumeric(v)
	if err := ctx.Err(); err != nil {
		return Value{}, err
	}
	if err := validateNumericFormat(v); err != nil {
		return Value{}, err
	}
	return v, nil
}
