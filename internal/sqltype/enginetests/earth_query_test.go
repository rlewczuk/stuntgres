package enginetests_test

import (
	"context"
	"errors"
	"fmt"
	"math"
	"testing"

	"stuntgres/internal/sqlstate"
	"stuntgres/internal/sqltype"
)

func earthQueryKernelTypes(t *testing.T) (cube, earth, floating *sqltype.Descriptor) {
	t.Helper()
	var err error
	cube, err = sqltype.Cube(49200, "cube", "public.cube")
	if err != nil {
		t.Fatal(err)
	}
	earth, err = sqltype.Domain(49201, "earth", "public.earth", cube.Usage())
	if err != nil {
		t.Fatal(err)
	}
	for _, d := range sqltype.Builtins() {
		if d.ID() == sqltype.Float8 {
			floating = d
		}
	}
	return
}

func TestEarthQueryKernel(t *testing.T) {
	ctx := context.Background()
	cube, earth, floating := earthQueryKernelTypes(t)
	point := func(text string) sqltype.Value {
		t.Helper()
		v, err := cube.Input(ctx, text, -1)
		if err != nil {
			t.Fatal(err)
		}
		v, err = earth.Wrap(v)
		if err != nil {
			t.Fatal(err)
		}
		return v
	}
	number := func(n float64) sqltype.Value {
		v, err := sqltype.Float(floating, n)
		if err != nil {
			t.Fatal(err)
		}
		return v
	}
	distance, err := sqltype.BindEarthCoordinate(sqltype.EarthDistance, earth, floating)
	if err != nil {
		t.Fatal(err)
	}
	box, err := sqltype.BindEarthCoordinate(sqltype.EarthBox, earth, floating)
	if err != nil {
		t.Fatal(err)
	}
	if distance.Formal(0) != earth || distance.Formal(1) != earth || distance.Result() != floating || box.Formal(0) != earth || box.Formal(1) != floating || box.Result() != cube || box.Formal(-1) != nil || box.Formal(2) != nil {
		t.Fatal("positional query ABI")
	}
	for _, tc := range []struct{ a, b, want string }{
		{"(6378168)", "(6378168,0,0)", "0.00000"},
		{"(6378168)", "(-6378168)", "20037605.73216"},
		{"(6378174)", "(-6378174)", "20037605.73216"},
		{"(6378168,0,NaN)", "(6378168)", "0.00000"},
	} {
		v, err := distance.Evaluate(ctx, []sqltype.Value{point(tc.a), point(tc.b)}, sqltype.InputEnvironment{})
		if err != nil {
			t.Fatal(err)
		}
		got, err := v.Float()
		if err != nil || math.IsNaN(got) || math.IsInf(got, 0) || fmt.Sprintf("%.5f", got) != tc.want || tc.want == "0.00000" && math.Signbit(got) {
			t.Fatal("independent distance witness", tc, got, err)
		}
	}
	center := point("(6378168)")
	for _, tc := range []struct {
		radius    float64
		want      string
		dimension int
	}{
		{0, "(6378168)", 1}, {-1, "(6378168)", 1}, {math.Inf(-1), "(6378168)", 1},
		{math.Inf(1), "(-6378168, -12756336, -12756336),(19134504, 12756336, 12756336)", 3},
		{math.NaN(), "(-6378168, -12756336, -12756336),(19134504, 12756336, 12756336)", 3},
	} {
		v, err := box.Evaluate(ctx, []sqltype.Value{center, number(tc.radius)}, sqltype.InputEnvironment{})
		if err != nil {
			t.Fatal(err)
		}
		text, err := cube.Output(ctx, v)
		n, dimensionErr := sqltype.CubeDimensionCount(cube, v)
		if err != nil || dimensionErr != nil || text != tc.want || n != tc.dimension || v.Type() != cube {
			t.Fatal("box bounds/type/dimension", text, n, err, dimensionErr)
		}
		if original, err := earth.Output(ctx, center); err != nil || original != "(6378168)" {
			t.Fatal("enlargement mutated its borrowed input", original, err)
		}
	}
	for _, p := range []sqltype.EarthCoordinatePlan{distance, box} {
		for i := range 2 {
			args := []sqltype.Value{center, center}
			if p.Operation() == sqltype.EarthBox {
				args[1] = number(1)
			}
			args[i] = sqltype.Null(p.Formal(i))
			v, err := p.Evaluate(ctx, args, sqltype.InputEnvironment{})
			if err != nil || !v.IsNull() || v.Type() != p.Result() {
				t.Fatal("strict typed NULL", i, err)
			}
			foreign := *p.Formal(i)
			args[i] = sqltype.Null(&foreign)
			if _, err := p.Evaluate(ctx, args, sqltype.InputEnvironment{}); err == nil {
				t.Fatal("strictness bypassed exact descriptor generation", i)
			}
		}
	}
	args := []sqltype.Value{sqltype.Null(earth), number(math.SmallestNonzeroFloat64)}
	if v, err := box.Evaluate(ctx, args, sqltype.InputEnvironment{}); err != nil || !v.IsNull() {
		t.Fatal("ordinary strict kernel", err)
	}
	_, err = box.EvaluateExpandedBox(ctx, args, sqltype.InputEnvironment{})
	var d *sqlstate.Error
	if !errors.As(err, &d) || d.Code != "22003" || d.Message != "value out of range: underflow" || d.Context != "" {
		t.Fatal("expanded conversion demand/leaf ownership", err)
	}
	for _, args := range [][]sqltype.Value{
		{sqltype.Null(earth), number(0)},
		{center, sqltype.Null(floating)},
		{sqltype.Null(earth), sqltype.Null(floating)},
	} {
		value, err := box.EvaluateExpandedBox(ctx, args, sqltype.InputEnvironment{})
		if err != nil || !value.IsNull() || value.Type() != cube {
			t.Fatal("expanded typed NULL neighbor", err)
		}
	}
	foreign := *earth
	if _, err := box.EvaluateExpandedBox(ctx, []sqltype.Value{sqltype.Null(&foreign), sqltype.Null(floating)}, sqltype.InputEnvironment{}); err == nil {
		t.Fatal("expanded NULL bypassed exact descriptor generation")
	}
	if _, err := distance.EvaluateExpandedBox(ctx, args, sqltype.InputEnvironment{}); err == nil {
		t.Fatal("non-box acquired expanded conversion route")
	}
}

func TestEarthQueryCancellation(t *testing.T) {
	cube, earth, floating := earthQueryKernelTypes(t)
	v, err := cube.Input(context.Background(), "(6378168)", -1)
	if err != nil {
		t.Fatal(err)
	}
	v, err = earth.Wrap(v)
	if err != nil {
		t.Fatal(err)
	}
	zero, _ := sqltype.Float(floating, 0)
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	for _, op := range []sqltype.EarthOperation{sqltype.EarthDistance, sqltype.EarthBox} {
		p, err := sqltype.BindEarthCoordinate(op, earth, floating)
		if err != nil {
			t.Fatal(err)
		}
		args := []sqltype.Value{v, v}
		if op == sqltype.EarthBox {
			args[1] = zero
		}
		if _, err := p.Evaluate(ctx, args, sqltype.InputEnvironment{}); !errors.Is(err, context.Canceled) {
			t.Fatal("canceled query kernel", err)
		}
		if op == sqltype.EarthBox {
			if _, err := p.EvaluateExpandedBox(ctx, []sqltype.Value{sqltype.Null(earth), zero}, sqltype.InputEnvironment{}); !errors.Is(err, context.Canceled) {
				t.Fatal("expanded NULL bypassed cancellation", err)
			}
		}
		if _, err := p.Evaluate(context.Background(), args, sqltype.InputEnvironment{}); err != nil {
			t.Fatal("kernel reused after cancellation", err)
		}
	}
}
