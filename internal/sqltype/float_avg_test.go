package sqltype

import (
	"errors"
	"math"
	"stuntgres/internal/sqlstate"
	"testing"
)

func TestFloatAvgArithmetic(t *testing.T) {
	types := testTypes()
	for _, tc := range []struct {
		id    ID
		input []float64
		want  float64
	}{
		{Float8, []float64{1, 3}, 2},
		{Float4, []float64{0.1}, float64(float32(0.1))},
		{Float4, []float64{math.MaxFloat32, math.MaxFloat32}, math.MaxFloat32},
		{Float8, []float64{1e200, 1e200}, 1e200},
		{Float8, []float64{1e308}, 1e308},
		{Float8, []float64{math.SmallestNonzeroFloat64, 0}, 0},
		{Float8, []float64{-math.SmallestNonzeroFloat64, 0}, math.Copysign(0, -1)},
		{Float8, []float64{math.Copysign(0, -1)}, 0},
		{Float8, []float64{math.Inf(1), 1}, math.Inf(1)},
		{Float8, []float64{math.Inf(-1), math.Inf(-1)}, math.Inf(-1)},
		{Float8, []float64{math.Inf(1), math.Inf(-1)}, math.NaN()},
		{Float8, []float64{math.NaN(), 1}, math.NaN()},
	} {
		var s FloatAvgState
		if err := s.Add(Null(types[tc.id])); err != nil {
			t.Fatal(err)
		}
		for _, x := range tc.input {
			v, err := Float(types[tc.id], x)
			if err != nil {
				t.Fatal(err)
			}
			if err := s.Add(v); err != nil {
				t.Fatal(err)
			}
		}
		before := [3]uint64{math.Float64bits(s.n), math.Float64bits(s.sx), math.Float64bits(s.sxx)}
		for range 2 {
			v, err := s.Result(types[Float8])
			if err != nil || v.Type() != types[Float8] {
				t.Fatal(v, err)
			}
			x, err := v.Float()
			if err != nil || (math.IsNaN(tc.want) && !math.IsNaN(x)) || (!math.IsNaN(tc.want) && math.Float64bits(x) != math.Float64bits(tc.want)) {
				t.Fatalf("result %g want %g: %v", x, tc.want, err)
			}
		}
		if before != ([3]uint64{math.Float64bits(s.n), math.Float64bits(s.sx), math.Float64bits(s.sxx)}) {
			t.Fatal("Result mutated transition state")
		}
	}
}

func TestFloatAvgTransitionAndOverflow(t *testing.T) {
	d := testTypes()[Float8]
	var s FloatAvgState
	for i, x := range []float64{1, 2, 3} {
		v, _ := Float(d, x)
		if err := s.Add(v); err != nil {
			t.Fatal(err)
		}
		want := [][3]float64{{1, 1, 0}, {2, 3, 0.5}, {3, 6, 2}}[i]
		if s.n != want[0] || s.sx != want[1] || s.sxx != want[2] {
			t.Fatal("Youngs-Cramer sequence", s)
		}
	}
	// Ordered units may witness roundoff without promising SQL row order.
	s = FloatAvgState{}
	for _, x := range []float64{9007199254740992, 1, -9007199254740992} {
		v, _ := Float(d, x)
		if err := s.Add(v); err != nil {
			t.Fatal(err)
		}
	}
	v, err := s.Result(d)
	x, _ := v.Float()
	if err != nil || x != 0 {
		t.Fatal("transition was replaced by exact/compensated summation", x, err)
	}
	for _, pair := range [][2]float64{{1e200, 0}, {1e308, 1e308}, {1e308, -1e308}} {
		s = FloatAvgState{}
		v, _ := Float(d, pair[0])
		if err := s.Add(v); err != nil {
			t.Fatal(err)
		}
		before := s
		v, _ = Float(d, pair[1])
		var diagnostic *sqlstate.Error
		err := s.Add(v)
		if !errors.As(err, &diagnostic) || diagnostic.Code != "22003" || diagnostic.Message != "value out of range: overflow" || diagnostic.Position != "" || diagnostic.Context != "" || diagnostic.Detail != "" || diagnostic.Hint != "" {
			t.Fatalf("overflow: %#v", err)
		}
		if s != before {
			t.Fatal("failed transition published state")
		}
	}
	// Existing infinity suppresses otherwise infinite Sxx, but finite inputs do not.
	s = FloatAvgState{}
	for _, x := range []float64{math.Inf(1), 1e308} {
		v, _ := Float(d, x)
		if err := s.Add(v); err != nil {
			t.Fatal(err)
		}
	}
	if !math.IsNaN(s.sxx) || !math.IsInf(s.sx, 1) {
		t.Fatal("infinity guard", s)
	}
}

func TestFloatAvgOwnership(t *testing.T) {
	types := testTypes()
	var s FloatAvgState
	v, err := s.Result(types[Float8])
	if err != nil || !v.IsNull() || v.Type() != types[Float8] {
		t.Fatal(v, err)
	}
	for _, invalid := range []Value{{}, Null(types[Numeric])} {
		if err := s.Add(invalid); err == nil {
			t.Fatal("invalid input accepted")
		}
	}
	if _, err := s.Result(types[Float4]); err == nil {
		t.Fatal("narrow result accepted")
	}
	var nilState *FloatAvgState
	if err := nilState.Add(Null(types[Float8])); err == nil {
		t.Fatal("nil state accepted")
	}
	if _, err := nilState.Result(types[Float8]); err == nil {
		t.Fatal("nil result state accepted")
	}
}
