package sqltype

import (
	"math"
	"math/big"
)

// StrtodUnderflow reports the glibc-2.36 ERANGE condition after conversion of a
// complete numeric token. nonzero describes the significand, not exponent digits.
// Normal results are allocation-free; only subnormal/minimum-normal boundaries
// need exact arithmetic. Callers retain responsibility for syntax and overflow.
// Match round_and_return, including its discarded pre-denormal half bit:
// https://raw.githubusercontent.com/bminor/glibc/glibc-2.36/stdlib/strtod_l.c
func StrtodUnderflow(raw string, parsed float64, nonzero bool) bool {
	if math.Abs(parsed) > 0x1p-1022 {
		return false
	}
	if parsed == 0 {
		return nonzero
	}
	// Keep the normal 53 mantissa bits, the original half bit, and an
	// independent sticky flag. Arbitrary precision is necessary only here.
	// Parse exactly before the single binary rounding: Float.Parse scales
	// decimal exponents with a rounded power of five, losing exactness.
	var exact big.Rat
	if _, ok := exact.SetString(raw); !ok {
		return true
	}
	var value big.Float
	value.SetPrec(54).SetMode(big.ToZero)
	value.SetRat(&exact)
	sticky := value.Acc() != big.Exact
	value.Abs(&value)
	var mantissa big.Float
	exponent := value.MantExp(&mantissa) - 1
	if exponent >= -1022 {
		return false
	}
	if exponent < -1075 {
		return true
	}
	mantissa.SetMantExp(&mantissa, 54)
	bits54, _ := mantissa.Uint64()
	bits53 := bits54 >> 1
	shift := uint(-1022 - exponent)
	// Tininess is tested after normal-precision nearest-even rounding.
	if shift == 1 && bits53 == (1<<53)-1 && bits54&1 != 0 {
		return false
	}
	// glibc-2.36 replaces, rather than retains, the original half bit
	// during denormalization. Exact subnormals therefore remain valid.
	return sticky || bits53&((uint64(1)<<shift)-1) != 0
}
