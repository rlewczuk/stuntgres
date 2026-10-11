package sqltype

import (
	"context"
	"math"
	"math/big"
)

func integerMagnitude(n int64) uint64 {
	if n < 0 {
		return uint64(-(n + 1)) + 1
	}
	return uint64(n)
}

func integerGcdLcm(d *Descriptor, a, b int64, lcm bool) (Value, error) {
	// Zero must precede magnitude and range checks, including signed minima.
	if lcm && (a == 0 || b == 0) {
		return Integer(d, 0)
	}
	left, right := integerMagnitude(a), integerMagnitude(b)
	x, y := left, right
	for y != 0 {
		x, y = y, x%y
	}
	limit, label := uint64(math.MaxInt64), "bigint"
	if d.id == Int4 {
		limit, label = math.MaxInt32, "integer"
	}
	if lcm {
		quotient := left / x
		if quotient > limit/right {
			return Value{}, failure("22003", label+" out of range")
		}
		x = quotient * right
	} else if x > limit {
		return Value{}, failure("22003", label+" out of range")
	}
	return Integer(d, int64(x))
}

func numericGcdLcm(ctx context.Context, a, b Value, lcm bool) (Value, error) {
	if err := ctx.Err(); err != nil {
		return Value{}, err
	}
	if a.special != numericFinite || b.special != numericFinite {
		return Value{typ: a.typ, special: numericNaN}, nil
	}
	scale := max(a.scale, b.scale)
	a.negative, b.negative = false, false
	if a.text == "" || b.text == "" {
		if lcm {
			return Value{typ: a.typ, scale: scale}, nil
		}
		if a.text == "" {
			a = b
		}
		a.scale = scale
		return a, nil
	}
	equal := compareNumeric(a, b) == 0
	if err := ctx.Err(); err != nil {
		return Value{}, err
	}
	if equal {
		a.scale = scale
		return a, nil
	}

	// Align normalized exponents once, never the retained display scales.
	// Each coefficient and power is private scratch; Values stay immutable.
	exponent := min(a.exponent, b.exponent)
	var left, right, power, ten, difference big.Int
	ten.SetInt64(10)
	left.SetString(a.text, 10)
	if a.exponent != exponent {
		difference.SetInt64(a.exponent - exponent)
		power.Exp(&ten, &difference, nil)
		left.Mul(&left, &power)
	}
	if err := ctx.Err(); err != nil {
		return Value{}, err
	}
	right.SetString(b.text, 10)
	if b.exponent != exponent {
		difference.SetInt64(b.exponent - exponent)
		power.Exp(&ten, &difference, nil)
		right.Mul(&right, &power)
	}
	if err := ctx.Err(); err != nil {
		return Value{}, err
	}

	x, y := &left, &right
	var savedLeft, savedRight, remainder big.Int
	if lcm {
		// Only lcm needs the original aligned coefficients after Euclid.
		savedLeft.Set(&left)
		savedRight.Set(&right)
	}
	r := &remainder
	for y.Sign() != 0 {
		if err := ctx.Err(); err != nil {
			return Value{}, err
		}
		r.Rem(x, y)
		x, y, r = y, r, x
	}
	if err := ctx.Err(); err != nil {
		return Value{}, err
	}
	var result expDecimal
	result.exponent = exponent
	if lcm {
		// The quotient cancels the gcd's common exponent: (A/G)*B
		// retains one factor of 10^exponent, just like the original b.
		result.coefficient.Quo(&savedLeft, x)
		if err := ctx.Err(); err != nil {
			return Value{}, err
		}
		result.coefficient.Mul(&result.coefficient, &savedRight)
	} else {
		// Transfer the last scratch coefficient: no mutable alias escapes and
		// none of the Euclidean temporaries is used again.
		result.coefficient = *x
	}
	return numericDecimalResult(ctx, a.typ, &result, scale)
}
