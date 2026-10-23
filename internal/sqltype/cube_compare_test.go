package sqltype

import (
	"context"
	"errors"
	"math"
	"slices"
	"strings"
	"testing"
)

func cubeComparisonUnitType(t *testing.T) *Descriptor {
	t.Helper()
	d, err := Cube(90001, "cube", "public.cube")
	if err != nil {
		t.Fatal(err)
	}
	return d
}
func cubeComparisonUnitValue(t *testing.T, d *Descriptor, text string) Value {
	t.Helper()
	v, err := d.Input(context.Background(), text, -1)
	if err != nil {
		t.Fatal(err)
	}
	return v
}
func TestCubeComparison(t *testing.T) {
	d := cubeComparisonUnitType(t)
	p, err := BindComparison(d, d, CollationRef{}, false)
	if err != nil {
		t.Fatal(err)
	}
	cases := []struct {
		left, right string
		sign        int
	}{
		{"(2)", "(1,100)", 1}, {"(0,5),(100,5)", "(0,6),(1,6)", -1},
		{"(0,0),(2,9)", "(0,0),(3,1)", -1}, {"(0),(2)", "(0,-1),(3,-1)", -1},
		{"(1,-1)", "(1)", -1}, {"(1,0)", "(1)", 1}, {"(1,0),(1,2)", "(1)", 1},
		{"()", "(0)", -1}, {"()", "()", 0}, {"(3,1),(1,4)", "(1,4),(3,1)", 0},
		{"(3,1),(1,4)", "(1,1),(3,4)", 0}, {"1", "[(1),(1)]", 0}, {"-0", "0", 0},
		{"-Infinity", "Infinity", -1}, {"0.10000000000000001", "0.10000000000000002", -1},
		{"NaN", "0", 0}, {"NaN", "1", 0}, {"(NaN),(1)", "1", 0}, {"(1),(NaN)", "0", 0},
		{"(0),(2)", "(0,1),(1,1)", 1}, {"(0,1),(1,1)", "(0,0),(3,0)", 1}, {"(0,0),(3,0)", "(0),(2)", 1},
		{"(" + strings.Repeat("1,", 99) + "1)", "(" + strings.Repeat("1,", 99) + "2)", -1},
		{"(" + strings.Repeat("1,", 99) + "1)", "(" + strings.Repeat("1,", 99) + "1)", 0},
	}
	for _, tc := range cases {
		t.Run(tc.left+"/"+tc.right, func(t *testing.T) {
			a, b := cubeComparisonUnitValue(t, d, tc.left), cubeComparisonUnitValue(t, d, tc.right)
			beforeA, beforeB := slices.Clone(a.cube.coordinates), slices.Clone(b.cube.coordinates)
			for _, reverse := range []bool{false, true} {
				left, right, want := a, b, tc.sign
				if reverse {
					left, right, want = b, a, -want
				}
				got, err := p.Compare(context.Background(), left, right, InputEnvironment{})
				if err != nil || got != want {
					t.Fatalf("sign %d want %d: %v", got, want, err)
				}
			}
			for i, n := range beforeA {
				if math.Float64bits(n) != math.Float64bits(a.cube.coordinates[i]) {
					t.Fatal("left payload mutated")
				}
			}
			for i, n := range beforeB {
				if math.Float64bits(n) != math.Float64bits(b.cube.coordinates[i]) {
					t.Fatal("right payload mutated")
				}
			}
		})
	}
	a := cubeComparisonUnitValue(t, d, "-0")
	if !math.Signbit(a.cube.coordinates[0]) {
		t.Fatal("signed-zero input lost")
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := p.Compare(ctx, a, a, InputEnvironment{}); !errors.Is(err, context.Canceled) {
		t.Fatal("cancellation", err)
	}
	if _, err := p.Compare(context.Background(), Null(d), a, InputEnvironment{}); err == nil {
		t.Fatal("NULL reached nonNULL comparator")
	}
	other, _ := Cube(90002, "cube", "other.cube")
	if _, err := BindComparison(d, other, CollationRef{}, false); err == nil {
		t.Fatal("foreign identity admitted")
	}
	bad := a
	bad.cube = &cubePayload{dimension: 2, point: true, coordinates: []float64{1}}
	if _, err := p.Compare(context.Background(), bad, a, InputEnvironment{}); err == nil {
		t.Fatal("corrupt payload admitted")
	}
}

func TestCubeGrouping(t *testing.T) {
	d := cubeComparisonUnitType(t)
	g, err := BindGrouping(d.Usage())
	if err != nil {
		t.Fatal(err)
	}
	for _, tc := range []struct {
		left, right string
		equal       bool
	}{
		{"(3,1),(1,4)", "(1,1),(3,4)", true}, {"1", "[(1),(1)]", true},
		{"-0", "0", true}, {"NaN", "0", true}, {"NaN", "1", true}, {"1", "2", false}, {"(1)", "(1,0)", false},
	} {
		a, b := cubeComparisonUnitValue(t, d, tc.left), cubeComparisonUnitValue(t, d, tc.right)
		equal, err := g.Equal(context.Background(), a, b)
		if err != nil || equal != tc.equal {
			t.Fatal(tc, equal, err)
		}
		ha, err := g.Hash(context.Background(), a)
		if err != nil {
			t.Fatal(err)
		}
		hb, err := g.Hash(context.Background(), b)
		if err != nil {
			t.Fatal(err)
		}
		if (tc.equal || a.cube.dimension == b.cube.dimension) && ha != hb {
			t.Fatal("dimension hash invariant", tc, ha, hb)
		}
	}
	if equal, err := g.Equal(context.Background(), Null(d), Null(d)); err != nil || !equal {
		t.Fatal("NULL grouping", equal, err)
	}
	if equal, err := g.Equal(context.Background(), Null(d), cubeComparisonUnitValue(t, d, "0")); err != nil || equal {
		t.Fatal("NULL/nonNULL grouping", equal, err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := g.Hash(ctx, Null(d)); !errors.Is(err, context.Canceled) {
		t.Fatal(err)
	}
}
