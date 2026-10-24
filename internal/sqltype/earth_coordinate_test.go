package sqltype

import (
	"context"
	"errors"
	"math"
	"stuntgres/internal/sqlstate"
	"testing"
)

func TestEarthCoordinateKernel(t *testing.T) {
	ctx := context.Background()
	types := testTypes()
	cube := cubeUnitType(t, 48200)
	earth, err := Domain(48201, "earth", "public.earth", cube.Usage())
	if err != nil {
		t.Fatal(err)
	}
	float := func(x float64) Value {
		v, err := Float(types[Float8], x)
		if err != nil {
			t.Fatal(err)
		}
		return v
	}
	plan := func(op EarthOperation) EarthCoordinatePlan {
		p, err := BindEarthCoordinate(op, earth, types[Float8])
		if err != nil {
			t.Fatal(err)
		}
		return p
	}
	assert := func(got, want float64) {
		t.Helper()
		if math.IsNaN(want) {
			if !math.IsNaN(got) {
				t.Fatalf("got %g want NaN", got)
			}
			return
		}
		if want == 0 {
			if got != 0 || math.Signbit(got) != math.Signbit(want) {
				t.Fatalf("got %g want signed %g", got, want)
			}
			return
		}
		tol := 2 * math.Abs(math.Nextafter(want, math.Inf(1))-want)
		if math.IsNaN(got) || math.Abs(got-want) > tol {
			t.Fatalf("got %.17g want %.17g", got, want)
		}
	}
	p := plan(EarthRadiusOperation)
	v, err := p.Evaluate(ctx, nil, InputEnvironment{})
	if err != nil {
		t.Fatal(err)
	}
	n, _ := v.Float()
	assert(n, 6378168)
	pi := float64(math.Pi)
	maximum := pi * 6378168
	for _, op := range []EarthOperation{EarthSecToGC, EarthGCToSec} {
		p := plan(op)
		for _, x := range []float64{-1, -math.SmallestNonzeroFloat64, math.Inf(-1), 0, math.Copysign(0, -1), math.NaN(), math.Inf(1), 12756336, math.Nextafter(12756336, 0), math.Nextafter(12756336, math.Inf(1)), maximum, math.Nextafter(maximum, 0), math.Nextafter(maximum, math.Inf(1))} {
			want := 0.0
			if !(x < 0) {
				if op == EarthSecToGC {
					if math.IsNaN(x) || x/12756336 > 1 {
						want = maximum
					} else {
						want = 12756336 * math.Asin(x/12756336)
					}
				} else {
					if math.IsNaN(x) || x/6378168 > pi {
						want = 12756336
					} else {
						want = 12756336 * math.Sin(x/12756336)
					}
				}
			}
			v, err := p.Evaluate(ctx, []Value{float(x)}, InputEnvironment{})
			if err != nil {
				t.Fatal(op, x, err)
			}
			got, _ := v.Float()
			assert(got, want)
		}
		_, err := p.Evaluate(ctx, []Value{float(math.SmallestNonzeroFloat64)}, InputEnvironment{})
		var d *sqlstate.Error
		if !errors.As(err, &d) || d.Code != "22003" || d.Message != "value out of range: underflow" {
			t.Fatal("underflow", err)
		}
		v, err := p.Evaluate(ctx, []Value{Null(types[Float8])}, InputEnvironment{})
		if err != nil || !v.IsNull() {
			t.Fatal("strict NULL", v, err)
		}
	}
	for _, tc := range []struct {
		op      EarthOperation
		x, want float64
	}{{EarthSecToGC, 10000, 10000.00102}, {EarthSecToGC, 100000, 100001.02426}, {EarthSecToGC, 1000000, 1001027.07131}, {EarthGCToSec, 10000, 9999.99898}, {EarthGCToSec, 100000, 99998.97577}, {EarthGCToSec, 1000000, 998976.08618}} {
		v, err := plan(tc.op).Evaluate(ctx, []Value{float(tc.x)}, InputEnvironment{})
		if err != nil {
			t.Fatal(err)
		}
		got, _ := v.Float()
		if math.Round(got*1e5)/1e5 != tc.want {
			t.Fatal("released rounded scalar", tc, got)
		}
	}
	for _, tc := range []struct {
		text string
		op   EarthOperation
		want float64
	}{{"(6378168)", EarthLatitude, 0}, {"(-6378168)", EarthLongitude, 180}, {"(-6378168,-0)", EarthLongitude, -180}, {"(0,0,6378174)", EarthLatitude, 90}, {"(0,0,-6378174)", EarthLatitude, -90}, {"(6378168,0,NaN)", EarthLatitude, 90}, {"(6378168,NaN)", EarthLongitude, math.NaN()}} {
		base, err := cube.Input(ctx, tc.text, -1)
		if err != nil {
			t.Fatal(err)
		}
		arg, err := earth.Wrap(base)
		if err != nil {
			t.Fatal(err)
		}
		v, err := plan(tc.op).Evaluate(ctx, []Value{arg}, InputEnvironment{})
		if err != nil {
			t.Fatal(err)
		}
		got, _ := v.Float()
		assert(got, tc.want)
	}
	ll := plan(EarthLL)
	for _, xy := range [][2]float64{{0, 0}, {45, 90}, {90, math.NaN()}} {
		v, err := ll.Evaluate(ctx, []Value{float(xy[0]), float(xy[1])}, InputEnvironment{})
		if err != nil {
			t.Fatal(err)
		}
		if v.typ != earth || v.cube.dimension != 3 || !v.cube.point {
			t.Fatal("domain/point shape", v)
		}
		base, _ := v.BaseValue()
		valid, err := base.CheckEarthCube(EarthOnSurface)
		if err != nil || !valid {
			t.Fatal("shared radius surface", err)
		}
	}
	for _, xy := range [][2]float64{{math.SmallestNonzeroFloat64, 0}, {0, math.SmallestNonzeroFloat64}, {math.Inf(1), 0}, {0, math.Inf(-1)}} {
		if _, err := ll.Evaluate(ctx, []Value{float(xy[0]), float(xy[1])}, InputEnvironment{}); err == nil {
			t.Fatal("invalid arithmetic admitted", xy)
		}
	}
	checked, err := Domain(48202, "earth", "public.earth", cube.Usage(), true)
	if err != nil {
		t.Fatal(err)
	}
	required, err := BindEarthCoordinate(EarthLL, checked, types[Float8])
	if err != nil {
		t.Fatal(err)
	}
	if _, err := required.Evaluate(ctx, []Value{float(0), float(0)}, InputEnvironment{}); err == nil {
		t.Fatal("unchecked domain result admitted")
	}
	foreign, err := Cube(cube.ID(), "cube", "public.cube")
	if err != nil {
		t.Fatal(err)
	}
	other, err := Domain(earth.ID(), "earth", "public.earth", foreign.Usage())
	if err != nil {
		t.Fatal(err)
	}
	input, _ := foreign.Input(ctx, "6378168", -1)
	input, _ = other.Wrap(input)
	if _, err := plan(EarthLatitude).Evaluate(ctx, []Value{input}, InputEnvironment{}); err == nil {
		t.Fatal("foreign domain generation admitted")
	}
	if _, err := BindEarthCoordinate(EarthLL, cube, types[Float8]); err == nil {
		t.Fatal("nondomain descriptor admitted")
	}
}
