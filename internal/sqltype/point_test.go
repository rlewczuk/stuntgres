package sqltype

import (
	"context"
	"math"
	"testing"
)

func TestPointInputOutput(t *testing.T) {
	d, ctx := testTypes()[Point], context.Background()
	for _, tc := range [][2]string{
		{"1,2", "(1,2)"}, {" ( 1 , 2 ) ", "(1,2)"}, {"0x1.8p1,0x1p-1", "(3,0.5)"},
		{"nan(payload),-inf", "(NaN,-Infinity)"}, {"-0,-0", "(-0,-0)"},
		{"1.0000000000000002,5e-324", "(1.0000000000000002,5e-324)"},
	} {
		v := boxUnitInput(t, d, tc[0])
		if v.geometry != nil {
			t.Fatal("point allocated geometry payload")
		}
		if out, err := d.Output(ctx, v); err != nil || out != tc[1] {
			t.Fatal(tc, out, err)
		}
	}
	for _, text := range []string{"", " ", "1", "1;2", "1,2,3", "[1,2]", "((1,2))", "(1,2", "1,2)", "(1,2)junk"} {
		_, err := d.Input(ctx, text, -1)
		lsegUnitError(t, err, "22P02", "invalid input syntax for type point: \""+text+"\"")
	}
	for _, tc := range [][2]string{{"1e400,2", "1e400"}, {"1e-4000,2", "1e-4000"}, {"(1e400;2)", "1e400"}} {
		_, err := d.Input(ctx, tc[0], -1)
		lsegUnitError(t, err, "22003", "\""+tc[1]+"\" is out of range for type double precision")
	}
	cancelled, cancel := context.WithCancel(ctx)
	cancel()
	if _, err := d.Input(cancelled, "1,2", -1); err != context.Canceled {
		t.Fatal(err)
	}
}

func TestPointCoordinateBits(t *testing.T) {
	types, ctx := testTypes(), context.Background()
	d, f := types[Point], types[Float8]
	if d.SubscriptElement() != f {
		t.Fatal("foreign coordinate descriptor")
	}
	for _, bits := range []uint64{0, 1 << 63, 0x7ff8000000000001, 0xfff8000000000002, 1} {
		n := math.Float64frombits(bits)
		v := pointValue(d, n, n)
		for _, index := range []int32{0, 1} {
			bounds := []ArraySubscriptValue{{Upper: index, HasUpper: true}}
			got, err := ArrayFetch(ctx, v, bounds)
			if err != nil || got.typ != f || math.Float64bits(got.floating) != bits {
				t.Fatal("coordinate bits", bits, index, got, err)
			}
			rhs, _ := Float(f, 9)
			changed, err := ArraySetElement(ctx, v, bounds, rhs)
			if err != nil {
				t.Fatal(err)
			}
			x, y := pointCoordinates(changed)
			if index == 0 {
				if x != 9 || math.Float64bits(y) != bits {
					t.Fatal("Y changed", bits, x, y)
				}
			} else if math.Float64bits(x) != bits || y != 9 {
				t.Fatal("X changed", bits, x, y)
			}
			x, y = pointCoordinates(v)
			if math.Float64bits(x) != bits || math.Float64bits(y) != bits {
				t.Fatal("mutated original")
			}
		}
	}
	v := pointValue(d, 1, 2)
	for _, bounds := range [][]ArraySubscriptValue{{{Upper: -1}}, {{Upper: 2}}, {{UpperNull: true}}, {{Upper: 0}, {Upper: 0}}} {
		got, err := ArrayFetch(ctx, v, bounds)
		if err != nil || !got.null || got.typ != f {
			t.Fatal("typed coordinate NULL", got, err)
		}
	}
	bounds := []ArraySubscriptValue{{Upper: 0}}
	rhs, _ := Float(f, 9)
	foreign, _ := Float(testTypes()[Float8], 9)
	if _, err := ArraySetElement(ctx, v, bounds, foreign); err == nil {
		t.Fatal("foreign coordinate accepted")
	}
	if got, err := ArraySetElement(ctx, v, bounds, Null(f)); err != nil || !SameDatum(got, v) {
		t.Fatal("NULL RHS", got, err)
	}
	if got, err := ArraySetElement(ctx, Null(d), bounds, rhs); err != nil || !got.null {
		t.Fatal("NULL base", got, err)
	}
	_, err := ArraySetElement(ctx, Null(d), []ArraySubscriptValue{{UpperNull: true}}, Null(f))
	lsegUnitError(t, err, "22004", "array subscript in assignment must not be null")
	if n := testing.AllocsPerRun(10, func() { _, _ = ArrayFetch(ctx, v, bounds) }); n != 0 {
		t.Fatal("coordinate fetch allocated", n)
	}
	if n := testing.AllocsPerRun(10, func() { _, _ = ArraySetElement(ctx, v, bounds, rhs) }); n != 0 {
		t.Fatal("coordinate update allocated", n)
	}
}
