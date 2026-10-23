package sqltype

import (
	"context"
	"errors"
	"testing"
)

func operatorUnitPlan(t *testing.T, op RangeOperator, a, b *Descriptor) RangeOperatorPlan {
	t.Helper()
	result := RangeOperatorResult(op, a, b)
	if result == nil {
		t.Fatal("missing unit signature", op, a.Name(), b.Name())
	}
	p, err := BindRangeOperator(op, result.Usage(), a.Usage(), b.Usage())
	if err != nil {
		t.Fatal(err)
	}
	return p
}
func TestRangeOperatorEndpointOrder(t *testing.T) {
	types := inspectUnitTypes()
	d := types[NumRange]
	// Every orientation at one subtype value, including the two equal inclusive
	// endpoints. This catches text ordering and lower/upper inclusivity collapse.
	endpoints := []struct {
		x     rangeDatum
		lower bool
	}{
		{inspectUnitInput(t, d, "[0,1)").rangeDatum, false},
		{inspectUnitInput(t, d, "[1,2)").rangeDatum, true},
		{inspectUnitInput(t, d, "[0,1]").rangeDatum, false},
		{inspectUnitInput(t, d, "(1,2)").rangeDatum, true},
	}
	ranks := []int{0, 1, 1, 2}
	for i, a := range endpoints {
		for j, b := range endpoints {
			cmp, err := d.compareRangeEndpoint(context.Background(), a.x, a.lower, b.x, b.lower)
			if err != nil {
				t.Fatal(err)
			}
			want := ranks[i] - ranks[j]
			if cmp < 0 != (want < 0) || cmp > 0 != (want > 0) {
				t.Fatal(i, j, cmp, want)
			}
		}
	}
	omitted := inspectUnitInput(t, d, "(,)")
	explicit := inspectUnitInput(t, d, "[-Infinity,NaN]")
	lower, err := d.compareRangeEndpoint(context.Background(), omitted.rangeDatum, true, explicit.rangeDatum, true)
	if err != nil {
		t.Fatal(err)
	}
	upper, err := d.compareRangeEndpoint(context.Background(), omitted.rangeDatum, false, explicit.rangeDatum, false)
	if err != nil {
		t.Fatal(err)
	}
	if lower >= 0 || upper <= 0 {
		t.Fatal("omission collapsed into explicit infinity")
	}
	empty := inspectUnitInput(t, d, "empty")
	lower, err = d.compareRangeValues(context.Background(), empty.rangeDatum, explicit.rangeDatum)
	if err != nil {
		t.Fatal(err)
	}
	upper, err = d.compareRangeValues(context.Background(), explicit.rangeDatum, empty.rangeDatum)
	if err != nil {
		t.Fatal(err)
	}
	if lower >= 0 || upper <= 0 {
		t.Fatal("empty order")
	}
	a, b := inspectUnitInput(t, d, "[1.0,2.00]"), inspectUnitInput(t, d, "[1.000,2.0]")
	cmp, err := d.compareRangeValues(context.Background(), a.rangeDatum, b.rangeDatum)
	if err != nil {
		t.Fatal(err)
	}
	if cmp != 0 || a.rangeDatum == b.rangeDatum {
		t.Fatal("semantic equality versus physical spelling")
	}
}
func TestRangeOperatorAuthorityNullAndAllocations(t *testing.T) {
	types, fresh := inspectUnitTypes(), inspectUnitTypes()
	ctx := context.Background()
	for _, id := range []ID{Int4Range, Int8Range, NumRange, DateRange, TimestampRange, TimestamptzRange, Int4Multirange, Int8Multirange, NumMultirange} {
		d := types[id]
		text := "[1,3)"
		switch id {
		case DateRange, TimestampRange, TimestamptzRange:
			text = "[2000-01-01,2000-01-03)"
		case Int4Multirange, Int8Multirange, NumMultirange:
			text = "{[1,3),[7,9)}"
		}
		a := inspectUnitInput(t, d, text)
		for op := RangeContains; op <= RangeDifference; op++ {
			p := operatorUnitPlan(t, op, d, d)
			result := p.result
			if _, err := BindRangeOperator(op, fresh[result.id].Usage(), d.Usage(), d.Usage()); err == nil {
				t.Fatal("foreign result", id, op)
			}
			if _, err := BindRangeOperator(op, result.Usage(), fresh[id].Usage(), d.Usage()); err == nil {
				t.Fatal("foreign operand", id, op)
			}
			if _, err := p.Evaluate(ctx, [2]Value{Null(fresh[id]), a}); err == nil {
				t.Fatal("foreign NULL value", id, op)
			}
			got, err := p.Evaluate(ctx, [2]Value{Null(d), a})
			if err != nil || !got.null || got.typ != result {
				t.Fatal("typed NULL", id, op, got, err)
			}
			canceled, cancel := context.WithCancel(ctx)
			cancel()
			if _, err := p.Evaluate(canceled, [2]Value{a, a}); !errors.Is(err, context.Canceled) {
				t.Fatal(err)
			}
			if op < RangeUnion {
				if n := testing.AllocsPerRun(100, func() {
					if _, err := p.Evaluate(ctx, [2]Value{a, a}); err != nil {
						panic(err)
					}
				}); n != 0 {
					t.Fatal("predicate allocation", id, op, n)
				}
			}
		}
		cmp, err := BindComparison(d, d, CollationRef{}, false)
		if err != nil {
			t.Fatal(err)
		}
		if n := testing.AllocsPerRun(100, func() {
			if _, err := cmp.Compare(ctx, a, a, InputEnvironment{}); err != nil {
				panic(err)
			}
		}); n != 0 {
			t.Fatal("comparison allocation", id, n)
		}
	}
	for _, pair := range [][2]*Descriptor{{types[Int4Range], types[Int8Range]}, {types[Int4Range], types[Int8Multirange]}, {types[Int4Range], types[Int8]}} {
		if RangeOperatorResult(RangeContains, pair[0], pair[1]) != nil {
			t.Fatal("wrong family admitted")
		}
	}
	for _, id := range []ID{Int4Range, Int8Range, NumRange, DateRange, TimestampRange, TimestamptzRange} {
		d := types[id]
		p := operatorUnitPlan(t, RangeContains, d, d.rangeSubtype)
		value := Null(d.rangeSubtype)
		if _, err := p.Evaluate(ctx, [2]Value{Null(d), value}); err != nil {
			t.Fatal(err)
		}
	}
}
func TestRangeOperatorDifferenceAndBoundSelection(t *testing.T) {
	types := inspectUnitTypes()
	d := types[NumRange]
	ctx := context.Background()
	for _, tc := range []struct {
		op      RangeOperator
		a, b, w string
		fails   bool
	}{
		{RangeUnion, "[1.0,2.00]", "[1.000,2.0]", "[1.000,2.0]", false},
		{RangeIntersection, "[1.0,2.00]", "[1.000,2.0]", "[1.0,2.00]", false},
		{RangeDifference, "[1,2]", "(1,2)", "", true},
		{RangeDifference, "[1,9)", "[3,7)", "", true},
		{RangeDifference, "[1,9)", "[1,3)", "[3,9)", false},
		{RangeDifference, "[1,9)", "[7,9)", "[1,7)", false},
		{RangeUnion, "(1,2)", "(2,3)", "", true},
	} {
		a, b := inspectUnitInput(t, d, tc.a), inspectUnitInput(t, d, tc.b)
		p := operatorUnitPlan(t, tc.op, d, d)
		got, err := p.Evaluate(ctx, [2]Value{a, b})
		if tc.fails {
			if err == nil {
				t.Fatal("missing representability error", tc)
			}
			continue
		}
		if err != nil {
			t.Fatal(err)
		}
		inspectUnitOutput(t, got, tc.w)
		inspectUnitOutput(t, a, tc.a)
		inspectUnitOutput(t, b, tc.b)
	}
}
