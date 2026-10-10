package sqltype

import (
	"math"
	"math/bits"
	"strconv"
)

// The dyadic rounding bounds below follow Ulf Adams' Ryu (Apache-2.0 or
// Boost-1.0), as used by PostgreSQL REL_18_0 src/common/{f2s,d2s}.c.
// PostgreSQL's STRICTLY_SHORTEST=0 excludes both endpoints. Go's Ryu supplies
// the nearest shortest candidate; only endpoint candidates need refinement.
// No tables or implementation code are copied from Ryu.
// Algorithm provenance: Copyright 2018 Ulf Adams,
// https://github.com/ulfjack/ryu and
// https://github.com/postgres/postgres/tree/REL_18_0/src/common.
// This is an independent bounded endpoint refinement, not a copied Ryu port;
// strconv owns the underlying Go Ryu conversion and its BSD-licensed tables.
func floatBounds(n float64, size int) (lower, upper uint64, exponent int) {
	var m, field uint64
	mantissa, bias := 52, 1023
	if size == 32 {
		mantissa, bias = 23, 127
		raw := uint64(math.Float32bits(float32(n)))
		m = raw & ((1 << 23) - 1)
		field = (raw >> 23) & 255
	} else {
		raw := math.Float64bits(n)
		m = raw & ((1 << 52) - 1)
		field = (raw >> 52) & 2047
	}
	shift := uint64(1)
	if m == 0 && field > 1 {
		shift = 0
	}
	if field == 0 {
		exponent = 1 - bias - mantissa - 2
	} else {
		m |= 1 << mantissa
		exponent = int(field) - bias - mantissa - 2
	}
	return 4*m - 1 - shift, 4*m + 2, exponent
}

// equalDecimalDyadic compares c*10^q and m*2^e exactly. After cancelling
// factors of five, both sides fit uint64 whenever equality is possible.
func equalDecimalDyadic(c uint64, q int, m uint64, e int) bool {
	if c == 0 || m == 0 {
		return c == m
	}
	z := bits.TrailingZeros64(c)
	c >>= z
	binaryExponent := q + z
	if q < 0 {
		for range -q {
			if c%5 != 0 {
				return false
			}
			c /= 5
		}
	} else {
		for range q {
			if c > ^uint64(0)/5 {
				return false
			}
			c *= 5
		}
	}
	z = bits.TrailingZeros64(m)
	m >>= z
	e += z
	return c == m && binaryExponent == e
}
func scientificParts(b []byte) (c uint64, q int) {
	digits := 0
	i := 0
	for ; i < len(b) && b[i] != 'e'; i++ {
		if b[i] != '.' {
			c = c*10 + uint64(b[i]-'0')
			digits++
		}
	}
	i++
	negative := b[i] == '-'
	i++
	exp := 0
	for ; i < len(b); i++ {
		exp = exp*10 + int(b[i]-'0')
	}
	if negative {
		exp = -exp
	}
	return c, exp - digits + 1
}
func appendScientific(dst []byte, c uint64, q int) []byte {
	dst = strconv.AppendUint(dst, c, 10)
	dst = append(dst, 'e')
	return strconv.AppendInt(dst, int64(q), 10)
}

// Nonpositive extra_float_digits selects significant-digit rounding of the
// exact stored IEEE value. Positive/unspecified output keeps the release's
// midpoint-excluding shortest path, also used for canonical SQL literals.
func floatOutputWithEnvironment(n float64, id ID, env OutputEnvironment) string {
	if !env.FloatPrecisionSet || env.ExtraFloatDigits > 0 || math.IsNaN(n) || math.IsInf(n, 0) {
		return floatOutput(n, id)
	}
	digits := 15
	if id == Float4 {
		digits = 6
	}
	digits = max(1, digits+int(env.ExtraFloatDigits))
	return strconv.FormatFloat(n, 'g', digits, 64)
}

func floatOutput(n float64, id ID) string {
	if math.IsNaN(n) {
		return "NaN"
	}
	if math.IsInf(n, 1) {
		return "Infinity"
	}
	if math.IsInf(n, -1) {
		return "-Infinity"
	}
	if n == 0 {
		if math.Signbit(n) {
			return "-0"
		}
		return "0"
	}
	negative := math.Signbit(n)
	n = math.Abs(n)
	size, limit, threshold := 64, 17, 15
	if id == Float4 {
		size, limit, threshold = 32, 9, 6
	}
	lower, upper, e := floatBounds(n, size)
	var scratch [64]byte
	candidate := strconv.AppendFloat(scratch[:0], n, 'e', -1, size)
	c, q := scientificParts(candidate)
	for precision := 1; ; precision++ {
		endpoint := 0
		if equalDecimalDyadic(c, q, lower, e) {
			endpoint = -1
		}
		if equalDecimalDyadic(c, q, upper, e) {
			endpoint = 1
		}
		if endpoint == 0 {
			break
		}
		adjacent := c
		if endpoint < 0 {
			adjacent++
		} else {
			adjacent--
		}
		trial := appendScientific(scratch[:0], adjacent, q)
		parsed, err := strconv.ParseFloat(string(trial), size)
		if err == nil && parsed == n && !equalDecimalDyadic(adjacent, q, lower, e) && !equalDecimalDyadic(adjacent, q, upper, e) {
			c = adjacent
			break
		}
		// Start at the actual shortest digit count, not at a fixed maximum.
		count := 0
		for v := c; v > 0; v /= 10 {
			count++
		}
		if precision <= count {
			precision = count + 1
		}
		if precision > limit {
			panic("float formatting precision bound")
		}
		candidate = strconv.AppendFloat(scratch[:0], n, 'e', precision-1, size)
		c, q = scientificParts(candidate)
	}
	for c%10 == 0 {
		c /= 10
		q++
	}
	var digits [24]byte
	text := strconv.AppendUint(digits[:0], c, 10)
	display := q + len(text) - 1
	var output [64]byte
	out := output[:0]
	if negative {
		out = append(out, '-')
	}
	if display >= -4 && display < threshold {
		point := len(text) + q
		if point <= 0 {
			out = append(out, '0', '.')
			for range -point {
				out = append(out, '0')
			}
			out = append(out, text...)
		} else if point >= len(text) {
			out = append(out, text...)
			for range point - len(text) {
				out = append(out, '0')
			}
		} else {
			out = append(out, text[:point]...)
			out = append(out, '.')
			out = append(out, text[point:]...)
		}
	} else {
		out = append(out, text[0])
		if len(text) > 1 {
			out = append(out, '.')
			out = append(out, text[1:]...)
		}
		out = append(out, 'e')
		if display < 0 {
			out = append(out, '-')
			display = -display
		} else {
			out = append(out, '+')
		}
		if display < 10 {
			out = append(out, '0')
		}
		out = strconv.AppendInt(out, int64(display), 10)
	}
	return string(out)
}
