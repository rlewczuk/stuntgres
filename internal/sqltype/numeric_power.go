package sqltype

import (
	"context"
	"math"
)

func numericIntegral(v Value) bool {
	return v.special == numericInfinity || v.special == numericNegativeInfinity || v.special == numericFinite && (v.text == "" || v.exponent >= 0)
}
func numericOdd(v Value) bool {
	return v.special == numericFinite && v.text != "" && v.exponent == 0 && (v.text[len(v.text)-1]-'0')%2 == 1
}
func numericInt32(v Value) (int64, bool) {
	if !numericIntegral(v) || v.special != numericFinite {
		return 0, false
	}
	if v.text == "" {
		return 0, true
	}
	if int64(len(v.text))+v.exponent > 10 {
		return 0, false
	}
	n := int64(0)
	for i := range v.text {
		n = n*10 + int64(v.text[i]-'0')
	}
	for range v.exponent {
		n *= 10
	}
	if v.negative {
		n = -n
	}
	return n, n >= math.MinInt32 && n <= math.MaxInt32
}

func numericPower(ctx context.Context, a, b Value) (Value, error) {
	one := Value{typ: a.typ, text: "1"}
	if a.special == numericNaN {
		if b.special == numericFinite && b.text == "" {
			return one, nil
		}
		return Value{typ: a.typ, special: numericNaN}, nil
	}
	if b.special == numericNaN {
		if a.special == numericFinite && !a.negative && a.text == "1" && a.exponent == 0 {
			return one, nil
		}
		return Value{typ: a.typ, special: numericNaN}, nil
	}
	as, bs := numericSign(a), numericSign(b)
	if as == 0 && bs < 0 {
		return Value{}, failure("2201F", "zero raised to a negative power is undefined")
	}
	if as < 0 && !numericIntegral(b) {
		return Value{}, failure("2201F", "a negative number raised to a non-integer power yields a complex result")
	}
	if a.special != numericFinite || b.special != numericFinite {
		if a.special == numericFinite && !a.negative && a.text == "1" && a.exponent == 0 || bs == 0 {
			return one, nil
		}
		if as == 0 {
			return Value{typ: a.typ}, nil
		}
		if b.special != numericFinite {
			absolute := a
			absolute.negative = false
			if a.special == numericFinite && compareNumeric(absolute, one) == 0 {
				return one, nil
			}
			greater := a.special != numericFinite || compareNumeric(absolute, one) > 0
			if greater == (bs > 0) {
				return Value{typ: a.typ, special: numericInfinity}, nil
			}
			return Value{typ: a.typ}, nil
		}
		if bs < 0 {
			return Value{typ: a.typ}, nil
		}
		special := numericInfinity
		if a.special == numericNegativeInfinity && numericOdd(b) {
			special = numericNegativeInfinity
		}
		return Value{typ: a.typ, special: special}, nil
	}
	if exponent, ok := numericInt32(b); ok {
		return numericPowerInteger(ctx, a, exponent, b.scale)
	}
	if a.text == "" {
		return Value{typ: a.typ, scale: 16}, nil
	}
	negative := a.negative && numericOdd(b)
	a.negative = false
	var base, exponent, logarithm, product, result expDecimal
	base.setValue(a)
	exponent.setValue(b)
	lnWeight := numericLnWeight(a)
	local := max(int64(0), 8-lnWeight)
	if err := decimalLn(ctx, &logarithm, &base, local); err != nil {
		return Value{}, err
	}
	var w expScratch
	w.multiply(&product, &logarithm, &exponent, local)
	estimate := decimalEstimate(&product)
	if math.Abs(estimate) > 6020 {
		if estimate > 0 {
			return Value{}, failure("22003", "value overflows numeric format")
		}
		return Value{typ: a.typ, scale: 1000}, ctx.Err()
	}
	weight := int64(estimate * 0.434294481903252)
	scale := min(int64(1000), max(int64(0), a.scale, b.scale, 16-weight))
	significant := max(int64(0), scale+weight)
	local = max(int64(0), significant-lnWeight+8)
	if err := decimalLn(ctx, &logarithm, &base, local); err != nil {
		return Value{}, err
	}
	w.multiply(&product, &logarithm, &exponent, local)
	if err := decimalExp(ctx, &product, &result, scale); err != nil {
		return Value{}, err
	}
	if negative {
		result.coefficient.Neg(&result.coefficient)
	}
	return numericDecimalResult(ctx, a.typ, &result, scale)
}

// Match power_var_int's base10000 leading-digit estimate and guarded squaring.
// The estimate chooses scales only, not a binary power result.
func numericPowerInteger(ctx context.Context, a Value, exponent, expScale int64) (Value, error) {
	f := float64(0)
	if a.text != "" {
		f = float64(exponent) * numericLog10Estimate(a, 4)
	}
	if f > 131072 {
		return Value{}, failure("22003", "value overflows numeric format")
	}
	if f+1 < -1000 {
		return Value{typ: a.typ, scale: 1000}, ctx.Err()
	}
	scale := min(int64(1000), max(int64(0), a.scale, expScale, 16-int64(f)))
	var base, result, one expDecimal
	base.setValue(a)
	base.coefficient.Abs(&base.coefficient)
	one.coefficient.SetInt64(1)
	var w expScratch
	switch exponent {
	case 0:
		result.coefficient.SetInt64(1)
	case 1:
		w.round(&result, &base.coefficient, base.exponent, scale)
	case -1:
		w.divide(&result, &one, &base, scale)
	case 2:
		w.multiply(&result, &base, &base, scale)
	default:
		if a.text == "" {
			return Value{typ: a.typ, scale: scale}, nil
		}
		significant := 1 + scale + int64(f) + int64(math.Log(math.Abs(float64(exponent)))) + 8
		magnitude := exponent
		if magnitude < 0 {
			magnitude = -magnitude
		}
		mask := uint64(magnitude)
		baseScale, resultScale := a.scale, int64(0)
		if mask&1 != 0 {
			result.coefficient.Set(&base.coefficient)
			result.exponent = base.exponent
			resultScale = a.scale
		} else {
			result.coefficient.SetInt64(1)
		}
		for mask >>= 1; mask > 0; mask >>= 1 {
			if err := ctx.Err(); err != nil {
				return Value{}, err
			}
			local := max(int64(0), min(significant-2*w.weight(&base)*4, 2*baseScale))
			w.multiply(&base, &base, &base, local)
			baseScale = local
			if mask&1 != 0 {
				local = max(int64(0), min(significant-(w.weight(&base)+w.weight(&result))*4, baseScale+resultScale))
				w.multiply(&result, &base, &result, local)
				resultScale = local
			}
			if w.weight(&base) > 32767 || w.weight(&result) > 32767 {
				if exponent > 0 {
					return Value{}, failure("22003", "value overflows numeric format")
				}
				return Value{typ: a.typ, scale: scale}, ctx.Err()
			}
		}
		if exponent < 0 {
			w.divide(&result, &one, &result, scale)
		} else {
			w.round(&result, &result.coefficient, result.exponent, scale)
		}
	}
	if a.negative && exponent%2 != 0 {
		result.coefficient.Neg(&result.coefficient)
	}
	return numericDecimalResult(ctx, a.typ, &result, scale)
}

// Leading up to groups base10000 digits; virtual zero groups are not expanded.
func numericLog10Estimate(a Value, groups int) float64 {
	weight, first := numericLeading(a.text, a.exponent)
	value := float64(first)
	power := weight * 4
	width := int64(len(a.text)) + a.exponent - 4*weight
	for group := 1; group < groups && width < int64(len(a.text)); group++ {
		next := int64(0)
		for i := int64(0); i < 4; i++ {
			next *= 10
			if width+i < int64(len(a.text)) {
				next += int64(a.text[width+i] - '0')
			}
		}
		value = value*10000 + float64(next)
		power -= 4
		width += 4
	}
	return math.Log10(value) + float64(power)
}

func numericLnWeight(a Value) int64 {
	low := Value{typ: a.typ, text: "9", exponent: -1}
	high := Value{typ: a.typ, text: "11", exponent: -1}
	if compareNumeric(a, low) >= 0 && compareNumeric(a, high) <= 0 {
		var x, minusOne expDecimal
		x.setValue(a)
		minusOne.coefficient.SetInt64(-1)
		var w expScratch
		w.add(&x, &minusOne)
		if x.coefficient.Sign() == 0 {
			return 0
		}
		x.coefficient.Abs(&x.coefficient)
		return w.digits(&x.coefficient) + x.exponent - 1
	}
	// ln(x) = log10(x)*ln(10); estimate near one is handled exactly above.
	logarithm := numericLog10Estimate(a, 2) * 2.302585092994046
	return int64(math.Log10(math.Abs(logarithm)))
}
