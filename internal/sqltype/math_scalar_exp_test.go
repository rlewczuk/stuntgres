package sqltype

import (
	"context"
	"math/big"
	"strings"
	"testing"
)

// Rational intervals, not the provider's decimal reduction/rounding schedule:
// |x| <= 1/16 makes the omitted tail smaller than twice its first term.
// Squaring positive endpoints preserves containment of the exact exponential.
func scalarExpOracle(t *testing.T, input string, scale int) string {
	t.Helper()
	x, ok := new(big.Rat).SetString(input)
	if !ok {
		t.Fatal(input)
	}
	k := 0
	for new(big.Rat).Abs(x).Cmp(big.NewRat(1, 16)) > 0 {
		x.Quo(x, big.NewRat(2, 1))
		k++
	}
	sum, term := big.NewRat(1, 1), big.NewRat(1, 1)
	for n := int64(1); n <= 4096; n++ {
		term.Mul(term, x)
		term.Quo(term, new(big.Rat).SetInt64(n))
		sum.Add(sum, term)
		next := new(big.Rat).Mul(term, x)
		next.Quo(next, new(big.Rat).SetInt64(n+1))
		err := new(big.Rat).Mul(new(big.Rat).Abs(next), big.NewRat(2, 1))
		lo, hi := new(big.Rat).Sub(sum, err), new(big.Rat).Add(sum, err)
		if lo.Sign() <= 0 {
			continue
		}
		for range k {
			lo.Mul(lo, lo)
			hi.Mul(hi, hi)
		}
		a, b := scalarRatDecimal(lo, scale), scalarRatDecimal(hi, scale)
		if a == b {
			return a
		}
	}
	t.Fatal("independent exponential interval failed to converge", input, scale)
	return ""
}

func scalarRatDecimal(x *big.Rat, scale int) string {
	power := new(big.Int).Exp(big.NewInt(10), big.NewInt(int64(scale)), nil)
	n := new(big.Int).Mul(x.Num(), power)
	q, r := new(big.Int), new(big.Int)
	q.QuoRem(n, x.Denom(), r)
	if new(big.Int).Lsh(new(big.Int).Abs(r), 1).Cmp(x.Denom()) >= 0 {
		if n.Sign() < 0 {
			q.Sub(q, big.NewInt(1))
		} else {
			q.Add(q, big.NewInt(1))
		}
	}
	negative := q.Sign() < 0
	digits := new(big.Int).Abs(q).String()
	if len(digits) <= scale {
		digits = strings.Repeat("0", scale+1-len(digits)) + digits
	}
	if scale > 0 {
		digits = digits[:len(digits)-scale] + "." + digits[len(digits)-scale:]
	}
	if negative {
		digits = "-" + digits
	}
	return digits
}

func TestMathScalarExpIndependent(t *testing.T) {
	for _, tc := range []struct {
		input string
		scale int
	}{
		{"0.000001", 16}, {"-0.000001", 16}, {"0.01", 16}, {"-0.01", 16}, {"0.0100001", 16}, {"0.02", 16}, {"0.04", 16},
		{"1.0", 16}, {"-1.0", 16}, {"2.5", 15}, {"-2.5", 17}, {"10.0", 12}, {"-10.0", 20},
		{"1." + strings.Repeat("0", 70), 70}, {"-1." + strings.Repeat("0", 70), 70},
	} {
		t.Run(tc.input, func(t *testing.T) {
			want := scalarExpOracle(t, tc.input, tc.scale)
			p, v := scalarProvider(t, "exp", Numeric, tc.input)
			got, err := p.Evaluate(context.Background(), v[:p.arity], InputEnvironment{})
			if err != nil {
				t.Fatal(err)
			}
			text, err := p.ResultUsage().Type.Output(context.Background(), got)
			if err != nil || text != want {
				t.Fatalf("exp(%s) got %q want %q: %v", tc.input, text, want, err)
			}
		})
	}
}
