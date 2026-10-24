package sqltype

import (
	"context"
	"fmt"
	"math"
	"testing"
)

func TestEarthPointKernel(t *testing.T) {
	types, ctx := testTypes(), context.Background()
	point, result := types[Point], types[Float8]
	p, err := BindEarthPoint(result.Usage(), point.Usage(), point.Usage())
	if err != nil {
		t.Fatal(err)
	}
	for _, tc := range []struct {
		a, b [2]float64
		want string
	}{
		{[2]float64{0, 0}, [2]float64{0, 0}, "0.00000"},
		{[2]float64{0, 0}, [2]float64{180, 0}, "12436.77274"},
		{[2]float64{0, 0}, [2]float64{0, 90}, "6218.38637"},
		{[2]float64{0, 0}, [2]float64{1, 0}, "69.09318"},
		{[2]float64{0, 30}, [2]float64{1, 30}, "59.83626"},
		{[2]float64{0, 60}, [2]float64{1, 60}, "34.54626"},
		{[2]float64{60, 0}, [2]float64{60, 1}, "69.09318"},
		{[2]float64{179, 0}, [2]float64{-179, 0}, "138.18636"},
		{[2]float64{87.6, 41.8}, [2]float64{106.7, 35.1}, "1129.18983"},
		{[2]float64{360, 0}, [2]float64{361, 0}, "69.09318"},
		{[2]float64{0, 100}, [2]float64{0, 100}, "0.00000"},
	} {
		values := [2]Value{pointValue(point, tc.a[0], tc.a[1]), pointValue(point, tc.b[0], tc.b[1])}
		for _, pair := range [][2]Value{values, {values[1], values[0]}} {
			v, err := p.Evaluate(ctx, pair)
			if err != nil || v.typ != result {
				t.Fatal("typed miles", v, err)
			}
			got, err := v.Float()
			if err != nil || fmt.Sprintf("%.5f", got) != tc.want || got == 0 && math.Signbit(got) {
				t.Fatal("miles", got, tc.want, err)
			}
		}
	}
	values := [2]Value{pointValue(point, 0, 0), pointValue(point, 0, 0.000000001)}
	v, err := p.Evaluate(ctx, values)
	if err != nil {
		t.Fatal(err)
	}
	if got, err := v.Float(); err != nil || got <= 0 || math.Abs(got-6.909318190000541e-8) > 3e-23 {
		t.Fatal("tiny arc", got, err)
	}
	for _, nonfinite := range []float64{math.NaN(), math.Inf(1), math.Inf(-1)} {
		for _, bad := range []Value{pointValue(point, nonfinite, 0), pointValue(point, 0, nonfinite)} {
			for _, pair := range [][2]Value{{bad, values[0]}, {values[0], bad}} {
				v, err := p.Evaluate(ctx, pair)
				if err != nil {
					t.Fatal(err)
				}
				if got, err := v.Float(); err != nil || !math.IsNaN(got) {
					t.Fatal("raw nonfinite", got, err)
				}
			}
		}
	}
	for _, pair := range [][2]Value{{Null(point), values[0]}, {values[0], Null(point)}, {Null(point), Null(point)}, {Null(point), pointValue(point, math.Inf(1), math.NaN())}} {
		v, err := p.Evaluate(ctx, pair)
		if err != nil || !v.null || v.typ != result {
			t.Fatal("strict FLOAT8", v, err)
		}
	}
	foreign := testTypes()
	for _, signature := range [][3]Usage{{foreign[Float8].Usage(), point.Usage(), point.Usage()}, {result.Usage(), point.Usage(), foreign[Point].Usage()}, {result.Usage(), types[Box].Usage(), types[Box].Usage()}, {point.Usage(), point.Usage(), point.Usage()}, {result.Usage(), Usage{Type: point, Typmod: 1}, point.Usage()}} {
		if _, err := BindEarthPoint(signature[0], signature[1], signature[2]); err == nil {
			t.Fatal("foreign signature admitted", signature)
		}
	}
	if _, err := p.Evaluate(ctx, [2]Value{Null(point), Null(foreign[Point])}); err == nil {
		t.Fatal("foreign NULL before strict")
	}
	cancelled, cancel := context.WithCancel(ctx)
	cancel()
	if _, err := p.Evaluate(cancelled, values); err != context.Canceled {
		t.Fatal(err)
	}
	if _, err := (EarthPointPlan{}).Evaluate(ctx, values); err == nil {
		t.Fatal("empty plan admitted")
	}
	if n := testing.AllocsPerRun(100, func() { _, _ = p.Evaluate(ctx, values) }); n != 0 {
		t.Fatal("mile kernel allocated", n)
	}
}
