package sqltype

import (
	"context"
	"errors"
	"math"
	"reflect"
	"strings"
	"testing"
)

func cubeSpatialUnitInput(t *testing.T, d *Descriptor, text string) Value {
	t.Helper()
	v, err := d.Input(context.Background(), text, -1)
	if err != nil {
		t.Fatal(err)
	}
	return v
}

func TestCubeSpatialPredicates(t *testing.T) {
	types := testTypes()
	d := cubeUnitType(t, 45000)
	cases := []struct {
		a, b                         string
		overlap, contains, contained bool
	}{
		{"(0,0),(1,1)", "(4,5),(6,7)", false, false, false},
		{"(0,0),(1,1)", "(1,1),(2,2)", true, false, false},
		{"(0,0),(4,4)", "(1,1),(2,2)", true, true, false},
		{"(1,1)", "(1,1)", true, true, true},
		{"(0,1),(0,3)", "(0,3),(0,5)", true, false, false},
		{"(3)", "(3,4)", false, false, true},
		{"(3)", "(3,0)", true, true, true},
		{"(0,5),(1,6)", "(0.5)", false, true, false},
		{"()", "(3,4)", false, false, true},
		{"()", "(0,0)", true, true, true},
		{"()", "()", true, true, true},
		{"(4,0),(0,4)", "(0,1),(0,4)", true, true, false},
		{"(NaN)", "(0)", true, true, true},
		{"(NaN),(1)", "(2)", false, false, false},
		{"(1),(NaN)", "(2)", true, true, true},
	}
	for _, tc := range cases {
		for _, reversed := range []bool{false, true} {
			a, b, contains, contained := tc.a, tc.b, tc.contains, tc.contained
			if reversed {
				a, b, contains, contained = b, a, contained, contains
			}
			values := [2]Value{cubeSpatialUnitInput(t, d, a), cubeSpatialUnitInput(t, d, b)}
			for i, want := range []bool{tc.overlap, contains, contained} {
				p, err := BindCubeSpatial(CubeSpatialOperation(i), types[Bool].Usage(), d.Usage(), d.Usage())
				if err != nil {
					t.Fatal(err)
				}
				v, err := p.Evaluate(context.Background(), values)
				if err != nil || v.Type() != types[Bool] || v.IsNull() || v.boolean != want {
					t.Fatalf("%s/%s op%d got%v/%v want%v", a, b, i, v, err, want)
				}
			}
		}
	}
}

func TestCubeSpatialDistances(t *testing.T) {
	types := testTypes()
	d := cubeUnitType(t, 45000)
	cases := []struct {
		a, b                          string
		euclidean, taxicab, chebyshev float64
	}{
		{"(0,0),(1,1)", "(4,5),(6,7)", 5, 7, 4},
		{"(1,1),(0,0)", "(6,7),(4,5)", 5, 7, 4},
		{"(0,0),(1,1)", "(1,1),(2,2)", 0, 0, 0},
		{"(3,4)", "()", 5, 7, 4},
		{"(0,5),(1,6)", "(0.5)", 5, 5, 5},
		{"(3)", "(3,4)", 4, 4, 4},
		{"()", "()", 0, 0, 0},
		{"(NaN),(1)", "(2)", 0, 0, 0},
		{"(1),(NaN)", "(2)", 0, 0, 0},
		{"(Infinity)", "(Infinity)", math.NaN(), math.NaN(), 0},
		{"(-Infinity)", "(-Infinity)", math.NaN(), math.NaN(), 0},
		{"(-Infinity)", "(Infinity)", math.Inf(1), math.Inf(1), math.Inf(1)},
		{"(NaN,Infinity,3)", "(0,Infinity,0)", math.NaN(), math.NaN(), 3},
		{"(1e200)", "(0)", math.Inf(1), 1e200, 1e200},
		{"(1e-200)", "(0)", 0, 1e-200, 1e-200},
		{"(1e308)", "(-1e308)", math.Inf(1), math.Inf(1), math.Inf(1)},
		{"(1e16,1,1)", "()", 1e16, 1e16, 1e16},
		{"(1,1,1e16)", "()", 1e16, 10000000000000002, 1e16},
		{"(-0)", "(0)", 0, 0, 0},
		{"(3,4," + strings.Repeat("0,", 97) + "0)", "()", 5, 7, 4},
	}
	for _, tc := range cases {
		for _, reverse := range []bool{false, true} {
			values := [2]Value{cubeSpatialUnitInput(t, d, tc.a), cubeSpatialUnitInput(t, d, tc.b)}
			if reverse {
				values[0], values[1] = values[1], values[0]
			}
			for i, want := range []float64{tc.euclidean, tc.taxicab, tc.chebyshev} {
				p, err := BindCubeSpatial(CubeSpatialOperation(int(CubeDistance)+i), types[Float8].Usage(), d.Usage(), d.Usage())
				if err != nil {
					t.Fatal(err)
				}
				v, err := p.Evaluate(context.Background(), values)
				if err != nil || v.Type() != types[Float8] || v.IsNull() || !(v.floating == want || math.IsNaN(want) && math.IsNaN(v.floating)) || want == 0 && math.Signbit(v.floating) {
					t.Fatalf("%s/%s metric%d got%v/%v want%v", tc.a, tc.b, i, v.floating, err, want)
				}
			}
		}
	}
}

func TestCubeSpatialPlan(t *testing.T) {
	types := testTypes()
	d, foreign := cubeUnitType(t, 45000), cubeUnitType(t, 45001)
	for op := CubeOverlap; op <= CubeChebyshev; op++ {
		result := types[op.ResultID()]
		p, err := BindCubeSpatial(op, result.Usage(), d.Usage(), d.Usage())
		if err != nil {
			t.Fatal(err)
		}
		for _, values := range [][2]Value{{Null(d), cubeSpatialUnitInput(t, d, "(1)")}, {cubeSpatialUnitInput(t, d, "(1)"), Null(d)}, {Null(d), Null(d)}} {
			v, err := p.Evaluate(context.Background(), values)
			if err != nil || !v.IsNull() || v.Type() != result {
				t.Fatal("typed strict NULL", v, err)
			}
		}
		for _, values := range [][2]Value{{Null(foreign), Null(d)}, {Value{typ: d}, Null(d)}, {Value{typ: d, cube: &cubePayload{dimension: 101}}, Null(d)}, {Value{typ: d, cube: &cubePayload{dimension: 1, point: true}}, Null(d)}} {
			if _, err := p.Evaluate(context.Background(), values); err == nil {
				t.Fatal("foreign/invalid payload admitted")
			}
		}
		for _, signature := range [][3]Usage{{result.Usage(), d.Usage(), foreign.Usage()}, {types[Text].Usage(), d.Usage(), d.Usage()}, {result.Usage(), types[Text].Usage(), types[Text].Usage()}, {result.Usage(), d.Usage(), {}}} {
			if _, err := BindCubeSpatial(op, signature[0], signature[1], signature[2]); err == nil {
				t.Fatal("invalid signature admitted")
			}
		}
	}
	if _, err := BindCubeSpatial(CubeSpatialOperation(255), types[Bool].Usage(), d.Usage(), d.Usage()); err == nil {
		t.Fatal("unknown operation admitted")
	}
	if _, err := (CubeSpatialPlan{}).Evaluate(context.Background(), [2]Value{}); err == nil {
		t.Fatal("zero plan admitted")
	}
	a, b := cubeSpatialUnitInput(t, d, "(3,-0),(1,4)"), cubeSpatialUnitInput(t, d, "(9,8)")
	before := append([]float64(nil), a.cube.coordinates...)
	p, _ := BindCubeSpatial(CubeDistance, types[Float8].Usage(), d.Usage(), d.Usage())
	if allocations := testing.AllocsPerRun(100, func() {
		if _, err := p.Evaluate(context.Background(), [2]Value{a, b}); err != nil {
			panic(err)
		}
	}); allocations != 0 {
		t.Fatalf("cube arithmetic allocated %g", allocations)
	}
	if !reflect.DeepEqual(before, a.cube.coordinates) || !math.Signbit(a.cube.coordinates[1]) {
		t.Fatal("arithmetic mutated payload")
	}
}

func TestCubeSpatialCancellation(t *testing.T) {
	d := cubeUnitType(t, 45000)
	types := testTypes()
	for op := CubeOverlap; op <= CubeChebyshev; op++ {
		p, err := BindCubeSpatial(op, types[op.ResultID()].Usage(), d.Usage(), d.Usage())
		if err != nil {
			t.Fatal(err)
		}
		ctx, cancel := context.WithCancel(context.Background())
		cancel()
		for _, values := range [][2]Value{{Null(d), Null(d)}, {cubeSpatialUnitInput(t, d, "(3,4)"), cubeSpatialUnitInput(t, d, "()")}} {
			if _, err := p.Evaluate(ctx, values); !errors.Is(err, context.Canceled) {
				t.Fatalf("cancellation precedence %v", err)
			}
		}
	}
}
