package sqltype

import (
	"context"
	"math"
	"testing"
)

func TestPointPredicates(t *testing.T) {
	types, ctx := testTypes(), context.Background()
	d := types[Point]
	for _, tc := range []struct {
		a, b [2]float64
		want [8]bool
	}{
		{[2]float64{0, 0}, [2]float64{0.0000005, 0}, [8]bool{true, false, false, false, false, false, true, true}},
		{[2]float64{0, 0}, [2]float64{0.000001, 0.000001}, [8]bool{true, false, false, false, false, false, true, true}},
		{[2]float64{0, 0}, [2]float64{0.000002, 0.000002}, [8]bool{false, true, true, false, true, false, false, false}},
		{[2]float64{0.000002, 0.000002}, [2]float64{0, 0}, [8]bool{false, true, false, true, false, true, false, false}},
		{[2]float64{math.NaN(), 1}, [2]float64{math.NaN(), 1}, [8]bool{true, false, false, false, false, false, true, false}},
		{[2]float64{math.NaN(), 1}, [2]float64{math.NaN(), 1.0000005}, [8]bool{false, true, false, false, false, false, true, false}},
		{[2]float64{math.Inf(1), 0}, [2]float64{math.Inf(1), math.Copysign(0, -1)}, [8]bool{true, false, false, false, false, false, true, true}},
	} {
		values := [2]Value{pointValue(d, tc.a[0], tc.a[1]), pointValue(d, tc.b[0], tc.b[1])}
		for op := PointSame; op <= PointVertical; op++ {
			p, err := BindPoint(op, types[Bool].Usage(), d.Usage(), d.Usage())
			if err != nil {
				t.Fatal(err)
			}
			v, err := p.Evaluate(ctx, values)
			if err != nil {
				t.Fatal(err)
			}
			truth, err := v.Boolean()
			if err != nil || truth != tc.want[op] {
				t.Fatal(op, truth, tc.want[op], err)
			}
			if p.Operation() != op || p.Name() != op.Name() {
				t.Fatal("plan identity")
			}
			if got, ok := PointKind(op.Routine()); !ok || got != op {
				t.Fatal("routine identity", op)
			}
			for _, pair := range [][2]Value{{Null(d), values[1]}, {values[0], Null(d)}, {Null(d), Null(d)}} {
				v, err := p.Evaluate(ctx, pair)
				if err != nil || !v.null || v.typ != types[Bool] {
					t.Fatal("strict result", v, err)
				}
			}
		}
	}
	for alias, want := range map[string]PointOperator{"!=": PointNotEqual, "<^": PointBelow, ">^": PointAbove} {
		if got, ok := PointKind(alias); !ok || got != want {
			t.Fatal(alias, got, ok)
		}
	}
	for _, name := range []string{"=", "<", "<=", ">", ">=", "point", "<->"} {
		if _, ok := PointKind(name); ok {
			t.Fatal("unsupported point operation", name)
		}
	}
	foreign := testTypes()
	for _, args := range [][3]Usage{{foreign[Bool].Usage(), d.Usage(), d.Usage()}, {types[Bool].Usage(), d.Usage(), foreign[Point].Usage()}, {d.Usage(), d.Usage(), d.Usage()}} {
		if _, err := BindPoint(PointSame, args[0], args[1], args[2]); err == nil {
			t.Fatal("foreign signature admitted")
		}
	}
	p, _ := BindPoint(PointSame, types[Bool].Usage(), d.Usage(), d.Usage())
	if _, err := p.Evaluate(ctx, [2]Value{Null(d), Null(foreign[Point])}); err == nil {
		t.Fatal("foreign NULL admitted")
	}
	cancelled, cancel := context.WithCancel(ctx)
	cancel()
	if _, err := p.Evaluate(cancelled, [2]Value{Null(d), Null(d)}); err != context.Canceled {
		t.Fatal(err)
	}
	if _, err := (PointPlan{}).Evaluate(ctx, [2]Value{}); err == nil {
		t.Fatal("empty plan admitted")
	}
	if equal, order := ComparisonCapabilities(d); equal || order {
		t.Fatal("invented default capabilities", equal, order)
	}
	for op := CompareEqual; op <= CompareGreaterEqual; op++ {
		_, err := BindComparisonPredicate(op, d, d, types[Bool], CollationRef{}, false)
		if (err == nil) != (op == CompareNotEqual) {
			t.Fatal("scalar admission", op, err)
		}
	}
	if _, err := BindGrouping(d.Usage()); err == nil {
		t.Fatal("point grouping admitted")
	}
	if _, err := BindOrdering(d.Usage()); err == nil {
		t.Fatal("point ordering admitted")
	}
	if _, ok := DefaultIndexClass(d); ok {
		t.Fatal("point default index admitted")
	}
	predicate, err := BindComparisonPredicate(CompareNotEqual, d, d, types[Bool], CollationRef{}, false)
	if err != nil {
		t.Fatal(err)
	}
	samePredicate, err := BindComparisonPredicate(CompareNotEqual, d, d, types[Bool], CollationRef{}, false)
	if err != nil || !predicate.Equal(samePredicate) {
		t.Fatal("retained point predicate identity", err)
	}
	value, err := predicate.Evaluate(ctx, pointValue(d, 0, 0), pointValue(d, 2, 3), InputEnvironment{})
	if err != nil {
		t.Fatal(err)
	}
	if truth, err := value.Boolean(); err != nil || !truth {
		t.Fatal("scalar inequality payload", truth, err)
	}
	if _, err := predicate.Evaluate(cancelled, Null(d), Null(d), InputEnvironment{}); err != context.Canceled {
		t.Fatal(err)
	}
}
