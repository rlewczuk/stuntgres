package sqltype

import (
	"context"
	"math"
	"strings"
	"testing"
)

func TestStatisticalAggregateNumericArithmetic(t *testing.T) {
	ctx := context.Background()
	types := testTypes()
	for _, c := range []struct {
		id             ID
		inputs         []string
		sample, stddev bool
		want           string
	}{
		{Int8, []string{"9007199254740992", "9007199254740994"}, false, false, "1.00000000000000000000"},
		{Int8, []string{"9007199254740992", "9007199254740994"}, true, false, "2.0000000000000000"},
		{Numeric, []string{"1", "2", "3"}, false, true, "0.81649658092772603273"},
		{Numeric, []string{"1.0000000000000000000000000", "3"}, false, false, "1." + strings.Repeat("0", 50)},
		{Numeric, []string{"9e131071", "9e131071"}, false, true, "0"},
		{Numeric, []string{"NaN", "1"}, true, true, "NaN"},
	} {
		var state NumericMomentsState
		for _, text := range c.inputs {
			if err := state.Add(ctx, numericAvgInput(t, types[c.id], text)); err != nil {
				t.Fatal(err)
			}
		}
		sum, squares := state.sum.sum.String(), state.squares.String()
		var held Value
		for range 2 {
			v, err := state.Result(ctx, types[Numeric], c.sample, c.stddev)
			if err != nil || numericOutput(v) != c.want {
				t.Fatalf("result %s want %s: %v", numericOutput(v), c.want, err)
			}
			held = v
		}
		if state.sum.sum.String() != sum || state.squares.String() != squares {
			t.Fatal("finalization mutated mathematical state")
		}
		if err := state.Add(ctx, numericAvgInput(t, types[c.id], "7")); err != nil {
			t.Fatal(err)
		}
		if numericOutput(held) != c.want {
			t.Fatal("retained result aliases scratch")
		}
	}
	var overflow NumericMomentsState
	for _, text := range []string{"9e131071", "-9e131071"} {
		if err := overflow.Add(ctx, numericAvgInput(t, types[Numeric], text)); err != nil {
			t.Fatal(err)
		}
	}
	_, err := overflow.Result(ctx, types[Numeric], false, false)
	numericAvgRange(t, err)
}

func TestStatisticalAggregateNumericCoefficientReuse(t *testing.T) {
	ctx := context.Background()
	d := testTypes()[Numeric]
	for _, inputs := range [][]string{
		{"-1.25", "0.000", "2.50", "10", "0.125"},
		{"0.125", "10", "2.50", "0.000", "-1.25"},
	} {
		var s NumericMomentsState
		for _, text := range inputs {
			if err := s.Add(ctx, numericAvgInput(t, d, text)); err != nil {
				t.Fatal(err)
			}
		}
		if err := s.Add(ctx, Null(d)); err != nil {
			t.Fatal(err)
		}
		// Original coefficients, not the sum-aligned scratch, must be squared.
		if s.count != 5 || s.sum.scale != 3 || s.sum.exponent != -3 ||
			s.sum.sum.String() != "11375" || s.squareExponent != -6 ||
			s.squares.String() != "107828125" {
			t.Fatal("coefficient/scale alignment", s.count, s.sum.scale,
				s.sum.sum.String(), s.sum.exponent, s.squares.String(), s.squareExponent)
		}
		const want = "16.3900000000000000" // (5*107.828125-11.375²)/25
		held, err := s.Result(ctx, d, false, false)
		if err != nil || numericOutput(held) != want {
			t.Fatal(numericOutput(held), err)
		}
		again, err := s.Result(ctx, d, false, false)
		if err != nil || numericOutput(again) != want {
			t.Fatal("repeated finalization", numericOutput(again), err)
		}
		if err := s.Add(ctx, numericAvgInput(t, d, "-3.0000")); err != nil {
			t.Fatal(err)
		}
		if _, err := s.Result(ctx, d, true, true); err != nil {
			t.Fatal(err)
		}
		if numericOutput(held) != want || numericOutput(again) != want {
			t.Fatal("retained result aliases coefficient or finalization scratch")
		}
	}
}

func TestStatisticalAggregateNumericOwnershipAndAllocations(t *testing.T) {
	ctx := context.Background()
	types := testTypes()
	var s NumericMomentsState
	for _, invalid := range []Value{{}, Null(types[Float8])} {
		if err := s.Add(ctx, invalid); err == nil {
			t.Fatal("invalid input accepted")
		}
	}
	if _, err := s.Result(ctx, types[Int8], false, false); err == nil {
		t.Fatal("invalid descriptor accepted")
	}
	one, _ := Integer(types[Int8], 1)
	for range 100 {
		if err := s.Add(ctx, one); err != nil {
			t.Fatal(err)
		}
	}
	if n := testing.AllocsPerRun(100, func() {
		if err := s.Add(ctx, one); err != nil {
			panic(err)
		}
	}); n != 0 {
		t.Fatalf("warmed integer allocations %g", n)
	}
	canceled, cancel := context.WithCancel(ctx)
	cancel()
	count := s.count
	if err := s.Add(canceled, one); err != context.Canceled || s.count != count {
		t.Fatal("canceled transition", err)
	}
	if v, err := s.Result(canceled, types[Numeric], false, true); err != context.Canceled || v.Type() != nil {
		t.Fatal("canceled result", err)
	}
	var singleton NumericMomentsState
	if err := singleton.Add(ctx, numericAvgInput(t, types[Numeric], "Infinity")); err != nil {
		t.Fatal(err)
	}
	v, err := singleton.Result(ctx, types[Numeric], true, true)
	if err != nil || !v.IsNull() {
		t.Fatal("sample cardinality must precede specials", err)
	}
}

func TestStatisticalAggregateFloatRegression(t *testing.T) {
	d := testTypes()[Float8]
	var s FloatRegressionState
	for i := 1; i <= 3; i++ {
		x, _ := Float(d, float64(i))
		y, _ := Float(d, float64(2*i))
		if err := s.Add(y, x); err != nil {
			t.Fatal(err)
		}
	}
	for op, want := range []float64{2, 2, 4, 8} {
		v, err := s.Result(d, RegressionOperation(op))
		got, _ := v.Float()
		if err != nil || got != want {
			t.Fatal(op, got, want, err)
		}
	}
	before := s
	huge, _ := Float(d, 1e200)
	one, _ := Float(d, 1)
	if err := s.Add(huge, one); err == nil || s != before {
		t.Fatal("unused-axis overflow not transactional", err)
	}
	if err := s.Add(Null(d), huge); err != nil || s != before {
		t.Fatal("pair NULL admission", err)
	}
	for _, specialX := range []bool{false, true} {
		var state FloatRegressionState
		special, _ := Float(d, math.Inf(1))
		x, y := one, special
		if specialX {
			x, y = special, one
		}
		if err := state.Add(y, x); err != nil {
			t.Fatal(err)
		}
		v, err := state.Result(d, RegressionSlope)
		if err != nil {
			t.Fatal(err)
		}
		if specialX {
			got, _ := v.Float()
			if !math.IsNaN(got) {
				t.Fatal("special X slope", got)
			}
		} else if !v.IsNull() {
			t.Fatal("finite singleton X must yield NULL slope")
		}
	}
	var unary FloatAvgState
	for _, f := range []float64{100000003, 100000004, 100000006, 100000007} {
		v, _ := Float(d, f)
		if err := unary.Add(v); err != nil {
			t.Fatal(err)
		}
	}
	v, err := unary.MomentsResult(d, false, false)
	got, _ := v.Float()
	if err != nil || got != 2.5 {
		t.Fatal("released unary transition", got, err)
	}
	var reciprocal FloatRegressionState
	for _, f := range []float64{1, 2, 4} {
		value, _ := Float(d, f)
		if err := reciprocal.Add(value, value); err != nil {
			t.Fatal(err)
		}
	}
	v, err = reciprocal.Result(d, RegressionSxx)
	got, _ = v.Float()
	if err != nil || math.Float64bits(got) != math.Float64bits(4.666666666666666) {
		t.Fatal("regression must multiply by reciprocal, not divide", got, err)
	}
}

func TestStatisticalAggregateRegressionFinalizers(t *testing.T) {
	d := testTypes()[Float8]
	nan := math.NaN()
	for _, tc := range []struct {
		name  string
		state FloatRegressionState
		want  [7]float64
		null  [7]bool
	}{
		{"empty", FloatRegressionState{}, [7]float64{}, [7]bool{true, true, true, true, true, true, true}},
		{"singleton", FloatRegressionState{n: 1, sx: 2, sy: 7}, [7]float64{0, 0, 0, 2, 7, 0, 0}, [7]bool{true, false, true, false, false, true, true}},
		{"constant x", FloatRegressionState{n: 3, sx: 6, sy: 12, syy: 8}, [7]float64{0, 0, 0, 2, 4, 0, 0}, [7]bool{true, false, false, false, false, true, true}},
		{"constant y", FloatRegressionState{n: 3, sx: 6, sxx: 2, sy: 6}, [7]float64{0, 0, 0, 2, 2, 2, 1}, [7]bool{true}},
		{"known", FloatRegressionState{n: 3, sx: 6, sxx: 2, sy: 15, syy: 8, sxy: 4}, [7]float64{1, 4.0 / 3, 2, 2, 5, 1, 1}, [7]bool{}},
		{"nonfinite x", FloatRegressionState{n: 1, sx: math.Inf(1), sxx: nan, sy: 1, sxy: nan}, [7]float64{0, nan, 0, math.Inf(1), 1, nan, 1}, [7]bool{true, false, true}},
		{"nonfinite y", FloatRegressionState{n: 1, sx: 1, sy: math.Inf(1), syy: nan, sxy: nan}, [7]float64{0, nan, 0, 1, math.Inf(1), 0, 0}, [7]bool{true, false, true, false, false, true, true}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			before := tc.state
			for i, op := range []RegressionOperation{RegressionCorr, RegressionCovarPop, RegressionCovarSamp, RegressionAvgx, RegressionAvgy, RegressionIntercept, RegressionR2} {
				for range 2 {
					v, err := tc.state.Result(d, op)
					if err != nil || v.IsNull() != tc.null[i] {
						t.Fatal(op, v, err)
					}
					if !v.IsNull() {
						got, err := v.Float()
						if err != nil || !(got == tc.want[i] || math.IsNaN(got) && math.IsNaN(tc.want[i])) {
							t.Fatal(op, got, tc.want[i], err)
						}
					}
				}
			}
			// Compare bits because NaN is intentionally not equal to itself.
			after := tc.state
			for i, value := range [6]float64{after.n, after.sx, after.sxx, after.sy, after.syy, after.sxy} {
				if math.Float64bits(value) != math.Float64bits([6]float64{before.n, before.sx, before.sxx, before.sy, before.syy, before.sxy}[i]) {
					t.Fatal("finalization mutated state")
				}
			}
		})
	}
}
