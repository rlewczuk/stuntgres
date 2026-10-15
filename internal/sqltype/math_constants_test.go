package sqltype

import (
	"context"
	"errors"
	"math"
	"testing"
)

func TestMathConstantsProvider(t *testing.T) {
	t.Run("ReviewBoundaries", mathConstantsGammaReviewBoundaries)
	t.Run("FactorResidual", func(t *testing.T) {
		high, low := gammaCompensatedProduct(.5, 0, 65, -math.Ldexp(1, -47))
		if high != 32.5 || low != -math.Ldexp(1, -48) {
			t.Fatalf("factor expansion lost: high=%.17g low=%.17g", high, low)
		}
	})
	t.Run("ReducedSineResidual", func(t *testing.T) {
		const piLow = 1.2246467991473532e-16
		high, low := gammaReducedSine(.25, piLow)
		// sqrt(2)/2 and its independent nearest-binary64 residual.
		// Degree23's remainder at pi/4 is below2e-28.
		if math.Float64bits(high) != 0x3fe6a09e667f3bcd || math.Abs(low-(-4.833646656726457e-17)) > 1e-27 {
			t.Fatalf("quarter sine high=%.17g low=%.17g", high, low)
		}
		high, low = gammaReducedSine(.5, piLow)
		if high != 1 || low != 0 {
			t.Fatalf("half sine high=%.17g low=%.17g", high, low)
		}
	})
	t.Run("SeedResidual", func(t *testing.T) {
		// Independent decimal Horner on the published P/Q coefficients,
		// not host Gamma or this kernel. This checks evaluation residuals;
		// the mathematical/public matrix separately checks approximation.
		for _, tc := range []struct {
			fraction float64
			bits     uint64
			low      float64
		}{
			{.125, 0x3fee22c196233d21, -6.645459518059175e-19},
			{.5, 0x3fec5bf891b4ef6b, -3.301351590091272e-17},
			{.75, 0x3fed68f5d0f97142, 2.929132164403139e-18},
		} {
			high, low := gammaPositiveSeed(tc.fraction)
			if math.Float64bits(high) != tc.bits || math.Abs(low-tc.low) > 1e-30 {
				t.Fatalf("seed(%g) high=%.17g bits=%016x low=%.17g want bits=%016x low=%.17g", tc.fraction, high, math.Float64bits(high), low, tc.bits, tc.low)
			}
		}
		for _, fraction := range []float64{0, 1} {
			high, low := gammaPositiveSeed(fraction)
			if high != 1 || low != 0 {
				t.Fatalf("seed endpoint(%g) high=%.17g low=%.17g", fraction, high, low)
			}
		}
	})
	// Finite targets are independent manual examples and pinned D1 values.
	for _, tc := range []struct {
		name, input string
		want        float64
		ulps        uint64
	}{
		{"pi", "", 3.141592653589793, 0},
		{"cbrt", "-8", -2, 2}, {"degrees", "0.5", 28.64788975654116, 2},
		{"radians", "45", .7853981633974483, 2},
		{"radians", "1.5e-322", math.Float64frombits(1), 0},
		{"erf", "1", .8427007929497149, 2}, {"erf", "-0", math.Copysign(0, -1), 0},
		{"erfc", "10", 2.088487583762545e-45, 2}, {"erfc", "27", 5.2370464393526292e-319, 2},
		{"erfc", "30", 0, 0}, {"gamma", "0.5", 1.772453850905516, 2},
		{"gamma", "-0.5", -3.544907701811032, 2}, {"gamma", "6", 120, 2},
		{"gamma", "-169.5", 5.6482208842233247e-306, 2},
		{"gamma", "-170.5", -3.3127395215386069e-308, 2},
		{"gamma", "-171.5", 1.9316265431711902e-310, 2},
		{"gamma", "-172.5", -1.1197835032887635e-312, 2},
		{"gamma", "-175.5", 2.1074864189004213e-319, 2},
		{"cbrt", "-0", math.Copysign(0, -1), 0},
		{"cbrt", "-Infinity", math.Inf(-1), 0},
		{"degrees", "-0", math.Copysign(0, -1), 0},
		{"degrees", "Infinity", math.Inf(1), 0},
		{"radians", "-0", math.Copysign(0, -1), 0},
		{"radians", "-Infinity", math.Inf(-1), 0},
		{"erf", "Infinity", 1, 0}, {"erf", "-Infinity", -1, 0},
		{"erfc", "Infinity", 0, 0}, {"erfc", "-Infinity", 2, 0},
		{"gamma", "Infinity", math.Inf(1), 0},
		{"exp", "-Infinity", 0, 0},
		// Independently evaluated decimal Stirling/reflection targets for exact
		// FLOAT8 inputs, including both representable sides of negative poles.
		{"gamma", "-171.25", 9.8910291950448e-310, 2},
		{"gamma", "-171.75", 7.5418350735583e-311, 2},
		{"gamma", "-171.00000000000003", 2.835121658031596e-296, 2},
		{"gamma", "-170.99999999999997", -2.835121658032425e-296, 2},
		{"gamma", "-172.00000000000003", -1.6483265453672064e-298, 2},
		{"gamma", "-171.99999999999997", 1.648326545367689e-298, 2},
		{"exp", "-744", math.Float64frombits(2), 0},
	} {
		t.Run(tc.name+tc.input, func(t *testing.T) {
			var inputs []string
			if tc.input != "" {
				inputs = []string{tc.input}
			}
			p, values := scalarProvider(t, tc.name, Float8, inputs...)
			got, err := p.Evaluate(context.Background(), values[:p.arity], InputEnvironment{})
			if err != nil || got.Type() != p.result.Type || got.IsNull() || !mathTrigMatches(got.floating, tc.want, tc.ulps) {
				t.Fatalf("%s(%s) actual=%g bits=%016x want=%g bits=%016x error=%v", tc.name, tc.input, got.floating, math.Float64bits(got.floating), tc.want, math.Float64bits(tc.want), err)
			}
		})
	}
	for _, tc := range []struct{ name, input, category string }{
		{"gamma", "0", "overflow"}, {"gamma", "-0", "overflow"}, {"gamma", "-1", "overflow"},
		{"gamma", "-Infinity", "overflow"}, {"gamma", "172", "overflow"},
		{"gamma", "-180.5", "underflow"}, {"gamma", "-1000.5", "underflow"},
		{"degrees", "1e308", "overflow"}, {"radians", "5e-324", "underflow"},
		{"exp", "-746", "underflow"},
	} {
		p, v := scalarProvider(t, tc.name, Float8, tc.input)
		got, err := p.Evaluate(context.Background(), v[:p.arity], InputEnvironment{})
		scalarError(t, err, "22003", "value out of range: "+tc.category)
		if got != (Value{}) {
			t.Fatal("failure published datum", got)
		}
	}
	for _, name := range []string{"cbrt", "degrees", "radians", "erf", "erfc", "gamma"} {
		p, v := scalarProvider(t, name, Float8, "NaN")
		got, err := p.Evaluate(context.Background(), v[:p.arity], InputEnvironment{})
		if err != nil || !math.IsNaN(got.floating) {
			t.Fatal(name, got, err)
		}
		got, err = p.Evaluate(context.Background(), []Value{Null(v[0].typ), Value{}}[:p.arity], InputEnvironment{})
		if err != nil || got != Null(p.result.Type) {
			t.Fatal(name, got, err)
		}
	}
}

func TestMathConstantsSignatures(t *testing.T) {
	types := mathTypes()
	u := types[Float8].Usage()
	for _, name := range []string{"pi", "cbrt", "degrees", "radians", "erf", "erfc", "gamma"} {
		args := []Usage{u}
		if name == "pi" {
			args = nil
		}
		p, err := BindMath(name, u, args...)
		if err != nil || p.Name() != name || p.ResultUsage() != u {
			t.Fatal(p, err)
		}
		for _, bad := range []struct {
			result Usage
			args   []Usage
		}{
			{Usage{}, args}, {types[Int4].Usage(), args}, {u, []Usage{u, u}},
			{u, []Usage{types[Numeric].Usage()}},
		} {
			if _, err := BindMath(name, bad.result, bad.args...); err == nil {
				t.Fatal("invalid signature accepted", name, bad)
			}
		}
		if name != "pi" {
			if _, err := BindMath(name, u); err == nil {
				t.Fatal("zero unary arity", name)
			}
			foreign := *types[Float8]
			if _, err := BindMath(name, u, foreign.Usage()); err == nil {
				t.Fatal("foreign argument owner", name)
			}
			if _, err := p.Evaluate(context.Background(), []Value{Null(&foreign), Value{}}[:p.arity], InputEnvironment{}); err == nil {
				t.Fatal("foreign datum owner", name)
			}
		} else if _, err := BindMath(name, u, u); err == nil {
			t.Fatal("pi operand accepted")
		}
		badUsage := u
		badUsage.Typmod = 3
		if _, err := BindMath(name, badUsage, args...); err == nil {
			t.Fatal("typmodded result accepted")
		}
	}
	if _, err := (MathPlan{}).Evaluate(context.Background(), nil, InputEnvironment{}); err == nil {
		t.Fatal("zero plan accepted")
	}
}

func TestMathConstantsCancellation(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	for _, name := range []string{"pi", "cbrt", "degrees", "radians", "erf", "erfc", "gamma"} {
		var inputs []string
		if name != "pi" {
			inputs = []string{"1"}
		}
		p, v := scalarProvider(t, name, Float8, inputs...)
		got, err := p.Evaluate(ctx, v[:p.arity], InputEnvironment{})
		if !errors.Is(err, context.Canceled) || got != (Value{}) {
			t.Fatal(name, got, err)
		}
	}
}
