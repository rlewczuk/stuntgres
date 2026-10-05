package sqltype

import (
	"math"
	"math/big"
	"strings"
	"testing"
)

// This test oracle deliberately uses exact rational IEEE neighbor averaging and
// integer decimal-grid ranges. It does not call the production bound, decimal
// parser, equality or candidate-selection helpers to derive its expectations.
func oracleFloatInterval(n float64, size int) (center, lower, upper *big.Rat) {
	n = math.Abs(n)
	center = new(big.Rat).SetFloat64(n)
	previous, next := math.Nextafter(n, 0), math.Nextafter(n, math.Inf(1))
	if size == 32 {
		previous = float64(math.Nextafter32(float32(n), 0))
		next = float64(math.Nextafter32(float32(n), float32(math.Inf(1))))
	}
	p := new(big.Rat).SetFloat64(previous)
	neighbor := new(big.Rat).SetFloat64(next)
	if math.IsInf(next, 1) {
		neighbor = new(big.Rat).Add(center, new(big.Rat).Sub(center, p))
	}
	lower = new(big.Rat).Quo(new(big.Rat).Add(center, p), big.NewRat(2, 1))
	upper = new(big.Rat).Quo(new(big.Rat).Add(center, neighbor), big.NewRat(2, 1))
	return
}
func oraclePower(base int64, exponent int) *big.Rat {
	magnitude := exponent
	if magnitude < 0 {
		magnitude = -magnitude
	}
	integer := new(big.Int).Exp(big.NewInt(base), big.NewInt(int64(magnitude)), nil)
	if exponent < 0 {
		return new(big.Rat).SetFrac(big.NewInt(1), integer)
	}
	return new(big.Rat).SetInt(integer)
}
func oracleDyadic(coefficient uint64, exponent int) *big.Rat {
	return new(big.Rat).Mul(new(big.Rat).SetInt(new(big.Int).SetUint64(coefficient)), oraclePower(2, exponent))
}
func oracleDecimalText(t *testing.T, text string) *big.Rat {
	t.Helper()
	r, ok := new(big.Rat).SetString(text)
	if !ok {
		t.Fatal("invalid decimal output", text)
	}
	return r.Abs(r)
}
func oracleAssertOpen(t *testing.T, n float64, size int, text string) {
	t.Helper()
	_, lower, upper := oracleFloatInterval(n, size)
	value := oracleDecimalText(t, text)
	if value.Cmp(lower) <= 0 || value.Cmp(upper) >= 0 {
		t.Fatalf("closed/outside interval size%d bits%x output%s", size, math.Float64bits(n), text)
	}
}

// Determine the first decimal grid containing any interior point, then choose
// the closest point exactly (ties: even coefficient). Search the adjacent
// display-exponent grids too: rounding near powers of ten can carry across them.
func oracleShortest(n float64, size int) (*big.Rat, int) {
	center, lower, upper := oracleFloatInterval(n, size)
	display := int(math.Floor(math.Log10(math.Abs(n))))
	for center.Cmp(oraclePower(10, display)) < 0 {
		display--
	}
	for center.Cmp(oraclePower(10, display+1)) >= 0 {
		display++
	}
	limit := 17
	if size == 32 {
		limit = 9
	}
	for precision := 1; precision <= limit; precision++ {
		var best, distance *big.Rat
		var bestCoefficient *big.Int
		minimum := new(big.Int).Exp(big.NewInt(10), big.NewInt(int64(precision-1)), nil)
		maximum := new(big.Int).Sub(new(big.Int).Mul(new(big.Int).Set(minimum), big.NewInt(10)), big.NewInt(1))
		for exponent := display - 1; exponent <= display+1; exponent++ {
			step := oraclePower(10, exponent-precision+1)
			scaledLower := new(big.Rat).Quo(lower, step)
			scaledUpper := new(big.Rat).Quo(upper, step)
			// Strict endpoints: floor(lower)+1 <= coefficient <= ceil(upper)-1.
			first := new(big.Int).Add(new(big.Int).Quo(scaledLower.Num(), scaledLower.Denom()), big.NewInt(1))
			last, remainder := new(big.Int), new(big.Int)
			last.QuoRem(scaledUpper.Num(), scaledUpper.Denom(), remainder)
			if remainder.Sign() == 0 {
				last.Sub(last, big.NewInt(1))
			}
			if first.Cmp(minimum) < 0 {
				first.Set(minimum)
			}
			if last.Cmp(maximum) > 0 {
				last.Set(maximum)
			}
			if first.Cmp(last) > 0 {
				continue
			}
			scaledCenter := new(big.Rat).Quo(center, step)
			floor := new(big.Int).Quo(scaledCenter.Num(), scaledCenter.Denom())
			for _, coefficient := range []*big.Int{new(big.Int).Set(floor), new(big.Int).Add(floor, big.NewInt(1))} {
				if coefficient.Cmp(first) < 0 {
					coefficient.Set(first)
				}
				if coefficient.Cmp(last) > 0 {
					coefficient.Set(last)
				}
				candidate := new(big.Rat).Mul(new(big.Rat).SetInt(coefficient), step)
				delta := new(big.Rat).Sub(candidate, center)
				delta.Abs(delta)
				if best == nil || delta.Cmp(distance) < 0 || delta.Cmp(distance) == 0 && coefficient.Bit(0) == 0 && bestCoefficient.Bit(0) != 0 {
					best, distance, bestCoefficient = candidate, delta, new(big.Int).Set(coefficient)
				}
			}
		}
		if best != nil {
			return best, precision
		}
	}
	panic("independent oracle exceeded IEEE decimal precision")
}

func TestFloatExactBoundsAndEquality(t *testing.T) {
	for _, size := range []int{32, 64} {
		var corpus []float64
		if size == 32 {
			for _, raw := range []uint32{1, 2, 0x007fffff, 0x00800000, 0x00800001, 0x3f800000, 0x3f800001, 0x4c000004, 0x7f7fffff} {
				corpus = append(corpus, float64(math.Float32frombits(raw)))
			}
		} else {
			for _, raw := range []uint64{1, 2, 0x000fffffffffffff, 0x0010000000000000, 0x0010000000000001, 0x3ff0000000000000, 0x3ff0000000000001, 0x4350000000000002, 0x7fefffffffffffff} {
				corpus = append(corpus, math.Float64frombits(raw))
			}
		}
		for _, n := range corpus {
			_, lower, upper := oracleFloatInterval(n, size)
			lo, hi, e := floatBounds(n, size)
			if oracleDyadic(lo, e).Cmp(lower) != 0 || oracleDyadic(hi, e).Cmp(upper) != 0 {
				t.Fatalf("size%d bounds%x", size, math.Float64bits(n))
			}
			// Exact decimal midpoint strings independently generated from rationals;
			// dyadic denominators terminate in decimal. Exercise both boundaries.
			for index, boundary := range []*big.Rat{lower, upper} {
				decimal := boundary.FloatString(1100)
				decimal = strings.TrimRight(strings.TrimRight(decimal, "0"), ".")
				if oracleDecimalText(t, decimal).Cmp(boundary) != 0 {
					t.Fatal("oracle endpoint text")
				}
				q := 0
				if point := strings.IndexByte(decimal, '.'); point >= 0 {
					q = -(len(decimal) - point - 1)
					decimal = decimal[:point] + decimal[point+1:]
				}
				for strings.HasSuffix(decimal, "0") {
					decimal = strings.TrimSuffix(decimal, "0")
					q++
				}
				coefficient, ok := new(big.Int).SetString(decimal, 10)
				if ok && coefficient.IsUint64() {
					endpoint := lo
					if index == 1 {
						endpoint = hi
					}
					if !equalDecimalDyadic(coefficient.Uint64(), q, endpoint, e) {
						t.Fatal("exact endpoint equality", size, n, index)
					}
					if equalDecimalDyadic(coefficient.Uint64()+1, q, endpoint, e) {
						t.Fatal("neighbor is not endpoint", size, n, index)
					}
				}
			}
		}
	}
	for _, tc := range []struct {
		c     uint64
		q     int
		m     uint64
		e     int
		equal bool
	}{{125, -3, 1, -3, true}, {5, -1, 1, -1, true}, {2500, -4, 1, -2, true}, {1, -1, 1, -3, false}, {15, 1, 75, 1, true}, {1 << 60, 1, 5, 61, true}, {100000000000000000, 4, 476837158203125, 21, true}, {1, 324, 1, 0, false}, {9, -2, 1, -3, false}} {
		exact := new(big.Rat).Mul(new(big.Rat).SetInt(new(big.Int).SetUint64(tc.c)), oraclePower(10, tc.q)).Cmp(oracleDyadic(tc.m, tc.e)) == 0
		if exact != tc.equal || equalDecimalDyadic(tc.c, tc.q, tc.m, tc.e) != tc.equal {
			t.Fatal(tc, exact)
		}
	}
	// Explicit asymmetric interval around one: lower half-ULP is smaller.
	_, lo, hi := oracleFloatInterval(1, 32)
	if lo.Cmp(big.NewRat(33554431, 33554432)) != 0 || hi.Cmp(big.NewRat(16777217, 16777216)) != 0 {
		t.Fatal(lo, hi)
	}
	_, lo, hi = oracleFloatInterval(1, 64)
	if lo.Cmp(new(big.Rat).Sub(big.NewRat(1, 1), oraclePower(2, -54))) != 0 || hi.Cmp(new(big.Rat).Add(big.NewRat(1, 1), oraclePower(2, -53))) != 0 {
		t.Fatal(lo, hi)
	}
	for _, tc := range []struct {
		n              float64
		size, exponent int
	}{{float64(math.SmallestNonzeroFloat32), 32, -150}, {math.SmallestNonzeroFloat64, 64, -1075}} {
		_, lo, hi = oracleFloatInterval(tc.n, tc.size)
		if lo.Cmp(oraclePower(2, tc.exponent)) != 0 || hi.Cmp(new(big.Rat).Mul(big.NewRat(3, 1), oraclePower(2, tc.exponent))) != 0 {
			t.Fatal(tc, lo, hi)
		}
	}
}

func TestFloatIndependentShortestSelection(t *testing.T) {
	check := func(n float64, id ID) {
		t.Helper()
		if n == 0 || math.IsNaN(n) || math.IsInf(n, 0) {
			return
		}
		size := 64
		if id == Float4 {
			size = 32
		}
		text := floatOutput(n, id)
		oracleAssertOpen(t, n, size, text)
		expected, precision := oracleShortest(n, size)
		got := oracleDecimalText(t, text)
		if got.Cmp(expected) != 0 {
			t.Fatalf("size%d bits%x: %s expected %s", size, math.Float64bits(n), text, expected.RatString())
		}
		digits := strings.TrimPrefix(text, "-")
		if i := strings.IndexByte(digits, 'e'); i >= 0 {
			digits = digits[:i]
		}
		digits = strings.ReplaceAll(digits, ".", "")
		digits = strings.Trim(digits, "0")
		if len(digits) != precision {
			t.Fatalf("nonshortest digits: %s precision%d", text, precision)
		}
		if math.Signbit(n) != strings.HasPrefix(text, "-") {
			t.Fatal("sign", text)
		}
	}
	for _, raw := range []uint32{1, 2, 3, 0x007fffff, 0x00800000, 0x00800001, 0x3f800000, 0x48951f84, 0x39800000, 0x4c000004, 0x7f7fffff} {
		n := float64(math.Float32frombits(raw))
		check(n, Float4)
		check(-n, Float4)
	}
	for _, raw := range []uint64{1, 2, 3, 0x000fffffffffffff, 0x0010000000000000, 0x0010000000000001, 0x3ff0000000000000, 0x4350000000000002, 0x7fefffffffffffff} {
		n := math.Float64frombits(raw)
		check(n, Float8)
		check(-n, Float8)
	}
	// Decimal carries, display transitions and their IEEE neighbors.
	for _, n := range []float64{0.0001, 0.001, 0.1, 1, 10, 1000000, 1e15, 1e16, 1e20} {
		check(n, Float8)
		check(math.Nextafter(n, 0), Float8)
		check(math.Nextafter(n, math.Inf(1)), Float8)
		f := float32(n)
		check(float64(f), Float4)
		check(float64(math.Nextafter32(f, 0)), Float4)
		check(float64(math.Nextafter32(f, float32(math.Inf(1)))), Float4)
	}
	for field := uint32(1); field < 255; field += 17 {
		for _, delta := range []int64{-1, 0, 1} {
			check(float64(math.Float32frombits(uint32(int64(field<<23)+delta))), Float4)
		}
	}
	for field := uint64(1); field < 2047; field += 101 {
		for _, delta := range []int64{-1, 0, 1} {
			check(math.Float64frombits(uint64(int64(field<<52)+delta)), Float8)
		}
	}
	seed := uint64(7341)
	for range 128 {
		seed = seed*6364136223846793005 + 1
		check(math.Float64frombits(seed), Float8)
		check(float64(math.Float32frombits(uint32(seed))), Float4)
	}
	// The release midpoint refinement must not be replaced by a shorter endpoint.
	for _, n := range []float64{math.Float64frombits(0x4350000000000002), -math.Float64frombits(0x4350000000000002)} {
		want := "1.8014398509481992e+16"
		if n < 0 {
			want = "-" + want
		}
		if got := floatOutput(n, Float8); got != want {
			t.Fatal(got, want)
		}
	}
}
