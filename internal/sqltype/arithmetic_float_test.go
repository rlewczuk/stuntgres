package sqltype

import (
	"context"
	"errors"
	"math"
	"math/big"
	"stuntgres/internal/sqlstate"
	"testing"
)

func TestFloatingArithmeticSignatures(t *testing.T) {
	types := testTypes()
	for _, op := range []ArithmeticOperator{ArithmeticAdd, ArithmeticSubtract, ArithmeticMultiply, ArithmeticDivide} {
		for _, ids := range [][3]ID{{Float4, Float4, Float4}, {Float4, Float8, Float8}, {Float8, Float4, Float8}, {Float8, Float8, Float8}} {
			p, err := BindArithmetic(op, types[ids[0]], types[ids[1]], types[ids[2]])
			if err != nil || p.ResultType() != types[ids[2]] {
				t.Fatal(ids, p, err)
			}
			a, _ := Float(types[ids[0]], 7.5)
			b, _ := Float(types[ids[1]], 2)
			v, err := p.Binary(context.Background(), a, b, InputEnvironment{})
			if err != nil || v.Type() != types[ids[2]] {
				t.Fatal(v, err)
			}
			v, err = p.Binary(context.Background(), Null(types[ids[0]]), b, InputEnvironment{})
			if err != nil || !v.IsNull() || v.Type() != types[ids[2]] {
				t.Fatal(v, err)
			}
		}
	}
	for _, id := range []ID{Float4, Float8} {
		for _, op := range []ArithmeticOperator{ArithmeticAdd, ArithmeticSubtract} {
			p, err := BindArithmetic(op, types[id], nil, types[id])
			if err != nil {
				t.Fatal(err)
			}
			a, _ := Float(types[id], math.Copysign(0, -1))
			v, err := p.Unary(context.Background(), a, InputEnvironment{})
			if err != nil || v.Type() != types[id] || math.Signbit(v.floating) != (op == ArithmeticAdd) {
				t.Fatal(v, err)
			}
		}
		if _, err := BindArithmetic(ArithmeticRemainder, types[id], types[id], types[id]); err == nil {
			t.Fatal("floating remainder bound")
		}
	}
	if _, err := BindArithmetic(ArithmeticPower, types[Float4], types[Float4], types[Float4]); err == nil {
		t.Fatal("float4 power bound")
	}
	p, _ := BindArithmetic(ArithmeticAdd, types[Float4], types[Float4], types[Float4])
	a, _ := Float(types[Float4], 0.1)
	b, _ := Float(types[Float4], 0.2)
	v, err := p.Binary(context.Background(), a, b, InputEnvironment{})
	if err != nil || math.Float32bits(float32(v.floating)) != 0x3e99999a {
		t.Fatal(v, err)
	}
	// Independently round exact rational quotients to binary32. This covers
	// normal/subnormal transition and large significand-neighbor divisions.
	p, _ = BindArithmetic(ArithmeticDivide, types[Float4], types[Float4], types[Float4])
	for _, bits := range [][2]uint32{{0x00800000, 0x40000000}, {0x007fffff, 0x3f800001}, {0x3f800001, 0x3f7fffff}, {0x7f7fffff, 0x40000000}, {0x00000002, 0x3fc00000}} {
		x, y := float64(math.Float32frombits(bits[0])), float64(math.Float32frombits(bits[1]))
		exact := new(big.Rat).Quo(new(big.Rat).SetFloat64(x), new(big.Rat).SetFloat64(y))
		rounded := new(big.Float).SetPrec(24).SetMode(big.ToNearestEven).SetRat(exact)
		want, _ := rounded.Float32()
		a, _ := Float(types[Float4], x)
		b, _ := Float(types[Float4], y)
		v, err := p.Binary(context.Background(), a, b, InputEnvironment{})
		if err != nil || math.Float32bits(float32(v.floating)) != math.Float32bits(want) {
			t.Fatal(bits, v, err, want)
		}
	}
	canceled, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := p.Binary(canceled, Null(types[Float4]), Null(types[Float4]), InputEnvironment{}); !errors.Is(err, context.Canceled) {
		t.Fatal(err)
	}
	if _, err := p.Binary(context.Background(), Null(types[Float8]), Null(types[Float4]), InputEnvironment{}); err == nil {
		t.Fatal("typed NULL concealed signature mismatch")
	}
}

func TestFloatingArithmeticResolution(t *testing.T) {
	types := testTypes()
	for _, op := range []ArithmeticOperator{ArithmeticAdd, ArithmeticSubtract, ArithmeticMultiply, ArithmeticDivide} {
		for _, known := range []ID{Int2, Int4, Int8, Numeric} {
			for _, reverse := range []bool{false, true} {
				l, r := types[Float4], types[known]
				want := ArithmeticSignature{Float4, Float8, Float8}
				if reverse {
					l, r = r, l
					want = ArithmeticSignature{Float8, Float4, Float8}
				}
				got, err := ResolveArithmetic(op, l, r, false)
				if err != nil || got != want {
					t.Fatal(op, known, reverse, got, err)
				}
			}
		}
		got, err := ResolveArithmetic(op, types[Float4], nil, false)
		if err != nil || got != (ArithmeticSignature{Float4, Float4, Float4}) {
			t.Fatal(got, err)
		}
	}
	for _, tc := range []struct {
		l, r ID
		want ArithmeticSignature
	}{{Int4, Int4, ArithmeticSignature{Float8, Float8, Float8}}, {Float4, Float4, ArithmeticSignature{Float8, Float8, Float8}}, {Numeric, Float4, ArithmeticSignature{Float8, Float8, Float8}}, {Numeric, Int4, ArithmeticSignature{Numeric, Numeric, Numeric}}} {
		got, err := ResolveArithmetic(ArithmeticPower, types[tc.l], types[tc.r], false)
		if err != nil || got != tc.want {
			t.Fatal(tc, got, err)
		}
	}
	for _, tc := range []struct {
		op    ArithmeticOperator
		l, r  *Descriptor
		unary bool
		code  string
	}{{ArithmeticRemainder, types[Float4], types[Float4], false, "42883"}, {ArithmeticAdd, nil, nil, false, "42725"}, {ArithmeticSubtract, nil, nil, true, "42725"}, {ArithmeticAdd, types[Bool], types[Float4], false, "42883"}} {
		_, err := ResolveArithmetic(tc.op, tc.l, tc.r, tc.unary)
		var d *sqlstate.Error
		if !errors.As(err, &d) || d.Code != tc.code {
			t.Fatal(tc, err)
		}
	}
}

func TestFloatingPowerRangeAndSpecials(t *testing.T) {
	for _, tc := range []struct{ x, y, want float64 }{{2, -1074, math.Float64frombits(1)}, {2, -1022, math.Float64frombits(0x0010000000000000)}, {-2, -1073, math.Float64frombits(0x8000000000000002)}, {math.NaN(), 0, 1}, {1, math.NaN(), 1}, {math.Inf(-1), -3, math.Copysign(0, -1)}, {-1, 1e300, 1}, {-1, 9007199254740991, -1}} {
		n, err := floatingPower(tc.x, tc.y)
		if err != nil || math.Float64bits(n) != math.Float64bits(tc.want) {
			t.Fatal(tc, n, err)
		}
	}
	for _, tc := range []struct {
		x, y          float64
		code, message string
	}{{0, -1, "2201F", "zero raised to a negative power is undefined"}, {-2, 0.5, "2201F", "a negative number raised to a non-integer power yields a complex result"}, {2, 1024, "22003", "value out of range: overflow"}, {2, -1075, "22003", "value out of range: underflow"}} {
		_, err := floatingPower(tc.x, tc.y)
		var d *sqlstate.Error
		if !errors.As(err, &d) || d.Code != tc.code || d.Message != tc.message || d.Position != "" {
			t.Fatal(tc, err)
		}
	}
}
