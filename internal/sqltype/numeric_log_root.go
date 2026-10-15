package sqltype

import "context"

// These wrappers follow REL_18_0 numeric.c's display-scale selection. Decimal
// kernels own the computation; estimates choose precision, never result digits.
func numericSqrt(ctx context.Context, a Value) (Value, error) {
	if numericSign(a) < 0 && a.special != numericNaN {
		return Value{}, failure("2201F", "cannot take square root of a negative number")
	}
	if a.special != numericFinite {
		return a, nil
	}
	var x, result expDecimal
	x.setValue(a)
	var w expScratch
	scale := min(int64(1000), max(int64(0), a.scale, 16-(w.weight(&x)*2+1)))
	if err := decimalSqrt(ctx, &result, &x, scale); err != nil {
		return Value{}, err
	}
	return numericDecimalResult(ctx, a.typ, &result, scale)
}

func numericLogDomain(a Value) error {
	if numericSign(a) < 0 {
		return failure("2201E", "cannot take logarithm of a negative number")
	}
	if numericSign(a) == 0 {
		return failure("2201E", "cannot take logarithm of zero")
	}
	return nil
}

func numericLn(ctx context.Context, a Value) (Value, error) {
	if a.special == numericNaN {
		return a, nil
	}
	if err := numericLogDomain(a); err != nil {
		return Value{}, err
	}
	if a.special == numericInfinity {
		return a, nil
	}
	scale := min(int64(1000), max(int64(0), a.scale, 16-numericLnWeight(a)))
	var x, result expDecimal
	x.setValue(a)
	if err := decimalLn(ctx, &result, &x, scale); err != nil {
		return Value{}, err
	}
	return numericDecimalResult(ctx, a.typ, &result, scale)
}

func numericLog(ctx context.Context, base, operand Value) (Value, error) {
	if base.special == numericNaN || operand.special == numericNaN {
		return Value{typ: base.typ, special: numericNaN}, nil
	}
	if base.special != numericFinite || operand.special != numericFinite {
		// Release special-value precedence checks both negatives before zeros.
		if numericSign(base) < 0 || numericSign(operand) < 0 {
			return Value{}, failure("2201E", "cannot take logarithm of a negative number")
		}
		if numericSign(base) == 0 || numericSign(operand) == 0 {
			return Value{}, failure("2201E", "cannot take logarithm of zero")
		}
		if base.special == numericInfinity {
			if operand.special == numericInfinity {
				return Value{typ: base.typ, special: numericNaN}, nil
			}
			return Value{typ: base.typ}, nil
		}
		return Value{typ: base.typ, special: numericInfinity}, nil
	}
	if err := numericLogDomain(base); err != nil {
		return Value{}, err
	}
	if err := numericLogDomain(operand); err != nil {
		return Value{}, err
	}
	baseWeight, numWeight := numericLnWeight(base), numericLnWeight(operand)
	resultWeight := numWeight - baseWeight
	scale := min(int64(1000), max(int64(0), base.scale, operand.scale, 16-resultWeight))
	var a, b, lnBase, lnNum, result expDecimal
	a.setValue(base)
	b.setValue(operand)
	if err := decimalLn(ctx, &lnBase, &a, max(int64(0), scale+resultWeight-baseWeight+8)); err != nil {
		return Value{}, err
	}
	if err := decimalLn(ctx, &lnNum, &b, max(int64(0), scale+resultWeight-numWeight+8)); err != nil {
		return Value{}, err
	}
	if lnBase.coefficient.Sign() == 0 {
		return Value{}, failure("22012", "division by zero")
	}
	var w expScratch
	w.divide(&result, &lnNum, &lnBase, scale)
	return numericDecimalResult(ctx, base.typ, &result, scale)
}
