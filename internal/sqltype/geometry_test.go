package sqltype

import (
	"context"
	"math"
	"strings"
	"testing"
)

func boxUnitInput(t *testing.T, d *Descriptor, text string) Value {
	t.Helper()
	v, err := d.Input(context.Background(), text, -1)
	if err != nil {
		t.Fatal(text, err)
	}
	return v
}

func TestBoxInputOutput(t *testing.T) {
	types := testTypes()
	d := types[Box]
	for _, text := range []string{"((1,4),(3,2))", "(3,2),(1,4)", "1,4,3,2", "(1,4,3,2)", "(1,4)(3,2)", "1,4,3,2,"} {
		v := boxUnitInput(t, d, text)
		out, err := d.Output(context.Background(), v)
		if err != nil || out != "(3,4),(1,2)" {
			t.Fatal(text, out, err)
		}
	}
	for _, text := range []string{"", "[1,2,3,4]", "1,2,3", "1,2,3,4,5", "(1,2),3,4", "(1;2),(3,4)"} {
		_, err := d.Input(context.Background(), text, -1)
		state, ok := err.(interface{ SQLState() string })
		if !ok || state.SQLState() != "22P02" || !strings.Contains(err.Error(), text) {
			t.Fatal(text, err)
		}
	}
	for _, text := range []string{"1e400,2,3,4", "1e-4000,2,3,4", "(1e400;2),(3,4)"} {
		_, err := d.Input(context.Background(), text, -1)
		state, ok := err.(interface{ SQLState() string })
		if !ok || state.SQLState() != "22003" {
			t.Fatal(text, err)
		}
	}
	negative := boxUnitInput(t, d, "-0,-0,-0,-0")
	for _, n := range negative.geometry.coordinates {
		if !math.Signbit(n) {
			t.Fatal("normalization erased signed zero")
		}
	}
	point := boxUnitInput(t, types[Point], "(3,4)")
	if out, err := types[Point].Output(context.Background(), point); err != nil || out != "(3,4)" {
		t.Fatal(out, err)
	}
	foreign := testTypes()[Box]
	retagged, err := negative.Retag(foreign)
	if err != nil || retagged.geometry != negative.geometry {
		t.Fatal("retag copied/changed immutable payload", err)
	}
	cancelled, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := d.Input(cancelled, "1,4,3,2", -1); err != context.Canceled {
		t.Fatal(err)
	}
}

func TestBoxComparison(t *testing.T) {
	types := testTypes()
	d, boolean := types[Box], types[Bool]
	a := boxUnitInput(t, d, "1,1,0,0")
	near := boxUnitInput(t, d, "1.0000005,1,0,0")
	nan := boxUnitInput(t, d, "NaN,1,0,0")
	for _, tc := range []struct {
		op   ComparisonOperator
		near bool
	}{
		{CompareEqual, true}, {CompareLess, false}, {CompareLessEqual, true}, {CompareGreater, false}, {CompareGreaterEqual, true},
	} {
		p, err := BindComparisonPredicate(tc.op, d, d, boolean, CollationRef{}, false)
		if err != nil {
			t.Fatal(err)
		}
		for _, pair := range []struct {
			left, right Value
			want        bool
		}{{a, near, tc.near}, {nan, a, false}, {a, nan, false}} {
			v, err := p.Evaluate(context.Background(), pair.left, pair.right, InputEnvironment{})
			if err != nil {
				t.Fatal(err)
			}
			truth, err := v.Boolean()
			if err != nil || truth != pair.want {
				t.Fatal(tc.op, truth, pair.want, err)
			}
		}
		v, err := p.Evaluate(context.Background(), Null(d), a, InputEnvironment{})
		if err != nil || !v.IsNull() || v.Type() != boolean {
			t.Fatal("strict NULL", v, err)
		}
	}
	if _, err := BindComparisonPredicate(CompareNotEqual, d, d, boolean, CollationRef{}, false); err == nil {
		t.Fatal("BOX gained <>")
	}
	if SameDatum(a, near) {
		t.Fatal("fuzzy equality leaked into datum identity")
	}
	if same, err := a.SamePayload(context.Background(), near); err != nil || same {
		t.Fatal("fuzzy equality leaked into payload identity", err)
	}
	if equal, ordering := ComparisonCapabilities(d); equal || ordering {
		t.Fatal("BOX gained default comparison")
	}
	if _, err := BindOrdering(d.Usage()); err == nil {
		t.Fatal("BOX gained ordering")
	}
	if _, err := BindGrouping(d.Usage()); err == nil {
		t.Fatal("BOX gained grouping")
	}
	for _, text := range []string{"1e308,1,-1e308,0", "1e200,1e200,0,0", "1e-200,1e-200,0,0"} {
		if _, err := boxArea(boxUnitInput(t, d, text)); err == nil {
			t.Fatal("unchecked BOX area", text)
		}
	}
}

func TestBoxComponents(t *testing.T) {
	types := testTypes()
	d, point := types[Box], types[Point]
	v := boxUnitInput(t, d, "3,4,1,2")
	bound := func(n int32) []ArraySubscriptValue { return []ArraySubscriptValue{{Upper: n, HasUpper: true}} }
	for _, i := range []int32{-1, 0, 1, 2} {
		got, err := ArrayFetch(context.Background(), v, bound(i))
		if err != nil || got.Type() != point || got.IsNull() != (i < 0 || i > 1) {
			t.Fatal(i, got, err)
		}
	}
	for _, bounds := range [][]ArraySubscriptValue{{{UpperNull: true}}, {{Upper: 0}, {Upper: 0}}} {
		got, err := ArrayFetch(context.Background(), v, bounds)
		if err != nil || !got.IsNull() || got.Type() != point {
			t.Fatal(got, err)
		}
	}
	rhs := boxUnitInput(t, point, "(-5,-6)")
	changed, err := ArraySetElement(context.Background(), v, bound(0), rhs)
	if err != nil {
		t.Fatal(err)
	}
	if out, err := d.Output(context.Background(), changed); err != nil || out != "(-5,-6),(1,2)" {
		t.Fatal(out, err)
	}
	if out, err := d.Output(context.Background(), v); err != nil || out != "(3,4),(1,2)" {
		t.Fatal("old payload mutated", out, err)
	}
	unchanged, err := ArraySetElement(context.Background(), changed, bound(0), Null(point))
	if err != nil || !SameDatum(changed, unchanged) {
		t.Fatal("NULL RHS changed BOX", err)
	}
	if got, err := ArraySetElement(context.Background(), Null(d), bound(0), rhs); err != nil || !got.IsNull() {
		t.Fatal("NULL BOX changed", err)
	}
	if _, err := ArraySetElement(context.Background(), Null(d), []ArraySubscriptValue{{UpperNull: true}}, Null(point)); err == nil {
		t.Fatal("NULL bound short-circuited")
	}
}

func TestBoxExactReplay(t *testing.T) {
	types := testTypes()
	d, ctx := types[Box], context.Background()
	for _, text := range []string{"(-5,-6),(8,9)", "(0.10000000000000002,-0),(5e-324,1)", "(NaN,Infinity),(-Infinity,0)"} {
		v, err := d.InputWithEnvironment(ctx, text, -1, InputEnvironment{RawBoxReplay: true})
		if err != nil {
			t.Fatal(err)
		}
		literal, err := d.SQLLiteral(ctx, v)
		if err != nil {
			t.Fatal(err)
		}
		for _, env := range []InputEnvironment{{RawBoxReplay: true}, {ExactSnapshot: true}} {
			replayed, err := d.InputWithEnvironment(ctx, strings.Trim(literal, "'"), -1, env)
			if err != nil || !SameDatum(v, replayed) {
				t.Fatal("exact BOX replay lost coordinate bits/order", literal, err)
			}
			if same, err := v.SamePayload(ctx, replayed); err != nil || !same {
				t.Fatal("separate exact payloads differ", err)
			}
		}
	}
	positive := boxUnitInput(t, d, "0,0,0,0")
	negative := boxUnitInput(t, d, "-0,-0,-0,-0")
	if SameDatum(positive, negative) {
		t.Fatal("signed zero identity erased")
	}
	if same, err := positive.SamePayload(ctx, negative); err != nil || same {
		t.Fatal("signed zero payload erased", err)
	}
	for _, axis := range []int{0, 1} {
		x, y := 0.0, 0.0
		if axis == 0 {
			x = math.Copysign(0, -1)
		} else {
			y = math.Copysign(0, -1)
		}
		p, n := pointValue(types[Point], 0, 0), pointValue(types[Point], x, y)
		if SameDatum(p, n) {
			t.Fatal("POINT signed zero datum identity erased", axis)
		}
		if same, err := p.SamePayload(ctx, n); err != nil || same {
			t.Fatal("POINT signed zero payload identity erased", axis, err)
		}
	}
	binaryNaN := math.Float64frombits(0x7ff8000000000002)
	point := pointValue(types[Point], binaryNaN, math.Copysign(0, -1))
	x, y := pointCoordinates(point)
	if math.Float64bits(x) != math.Float64bits(binaryNaN) || !math.Signbit(y) {
		t.Fatal("inline POINT changed coordinate bits")
	}
}
