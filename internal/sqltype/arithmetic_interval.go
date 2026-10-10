package sqltype

import "math"

func arithmeticIntervalInfinity(d *Descriptor, sign int) (Value, error) {
	if sign < 0 {
		return IntervalValue(d, math.MinInt32, math.MinInt32, math.MinInt64)
	}
	return IntervalValue(d, math.MaxInt32, math.MaxInt32, math.MaxInt64)
}

func negateInterval(v Value, d *Descriptor) (Value, error) {
	if sign := intervalInfinity(v); sign != 0 {
		return arithmeticIntervalInfinity(d, -sign)
	}
	if v.months == math.MinInt32 || v.days == math.MinInt32 || v.integer == math.MinInt64 || intervalReserved(-v.months, -v.days, -v.integer) {
		return Value{}, failure("22008", "interval out of range")
	}
	return IntervalValue(d, -v.months, -v.days, -v.integer)
}

func addIntervals(a, b Value, d *Descriptor, subtract bool) (Value, error) {
	ai, bi := intervalInfinity(a), intervalInfinity(b)
	if subtract {
		bi = -bi
	}
	if ai != 0 || bi != 0 {
		if ai != 0 && bi != 0 && ai != bi {
			return Value{}, failure("22008", "interval out of range")
		}
		if ai != 0 {
			return arithmeticIntervalInfinity(d, ai)
		}
		return arithmeticIntervalInfinity(d, bi)
	}
	m, day := int64(a.months), int64(a.days)
	var u int64
	var ok bool
	if subtract {
		m -= int64(b.months)
		day -= int64(b.days)
		u, ok = arithmeticSubtractInt64(a.integer, b.integer)
	} else {
		m += int64(b.months)
		day += int64(b.days)
		u, ok = intervalAdd(a.integer, b.integer)
	}
	if !ok || m < math.MinInt32 || m > math.MaxInt32 || day < math.MinInt32 || day > math.MaxInt32 || intervalReserved(int32(m), int32(day), u) {
		return Value{}, failure("22008", "interval out of range")
	}
	return IntervalValue(d, int32(m), int32(day), u)
}

func scaleInterval(v Value, factor float64, d *Descriptor, divide bool) (Value, error) {
	if divide && factor == 0 {
		return Value{}, failure("22012", "division by zero")
	}
	if math.IsNaN(factor) {
		return Value{}, failure("22008", "interval out of range")
	}
	infinity := intervalInfinity(v)
	if infinity != 0 {
		if factor == 0 || divide && math.IsInf(factor, 0) {
			return Value{}, failure("22008", "interval out of range")
		}
		if factor < 0 {
			infinity = -infinity
		}
		return arithmeticIntervalInfinity(d, infinity)
	}
	if math.IsInf(factor, 0) {
		if divide {
			return IntervalValue(d, 0, 0, 0)
		}
		hi, lo := intervalSpan(v)
		if hi == 0 && lo == 0 {
			return Value{}, failure("22008", "interval out of range")
		}
		sign := 1
		if hi < 0 {
			sign = -sign
		}
		if factor < 0 {
			sign = -sign
		}
		return arithmeticIntervalInfinity(d, sign)
	}
	return scaleFiniteInterval(v.months, v.days, v.integer, factor, d, divide)
}

// Multiplication and division use their own float8 operations, not reciprocals.
// AVG delegates only its finite division stage; aggregation state stays separate.
func scaleFiniteInterval(month, day int32, clock int64, factor float64, d *Descriptor, divide bool) (Value, error) {
	var qm, qd, qt float64
	if divide {
		qm, qd, qt = float64(month)/factor, float64(day)/factor, float64(clock)/factor
	} else {
		qm, qd, qt = float64(float64(month)*factor), float64(float64(day)*factor), float64(float64(clock)*factor)
	}
	if math.IsNaN(qm) || qm < -2147483648.0 || qm >= 2147483648.0 || math.IsNaN(qd) || qd < -2147483648.0 || qd >= 2147483648.0 {
		return Value{}, failure("22008", "interval out of range")
	}
	months, days := int32(qm), int32(qd)
	monthRemainder := math.RoundToEven((qm-float64(months))*30*1e6) / 1e6
	secondRemainder := math.RoundToEven((qd-float64(days)+monthRemainder-math.Trunc(monthRemainder))*86400*1e6) / 1e6
	daySum := int64(days)
	if math.Abs(secondRemainder) >= 86400 {
		carry := int64(secondRemainder / 86400)
		daySum += carry
		if daySum < math.MinInt32 || daySum > math.MaxInt32 {
			return Value{}, failure("22008", "interval out of range")
		}
		secondRemainder -= float64(carry) * 86400
	}
	daySum += int64(monthRemainder)
	if daySum < math.MinInt32 || daySum > math.MaxInt32 {
		return Value{}, failure("22008", "interval out of range")
	}
	// The explicit conversion requires a rounded product, excluding fused FMA.
	fractionMicros := float64(secondRemainder * 1e6)
	micros := math.RoundToEven(qt + fractionMicros)
	if math.IsNaN(micros) || micros < -9223372036854775808.0 || micros >= 9223372036854775808.0 || intervalReserved(months, int32(daySum), int64(micros)) {
		return Value{}, failure("22008", "interval out of range")
	}
	return IntervalValue(d, months, int32(daySum), int64(micros))
}
