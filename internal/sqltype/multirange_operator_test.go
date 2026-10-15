package sqltype

import (
	"context"
	"errors"
	"slices"
	"testing"
)

func TestMultirangeOperatorSetsAndImmutability(t *testing.T) {
	ctx := context.Background()
	types := inspectUnitTypes()
	for _, id := range []ID{Int4Multirange, Int8Multirange, NumMultirange} {
		d := types[id]
		for _, tc := range []struct {
			op      RangeOperator
			a, b, w string
		}{
			{RangeUnion, "{[1,3),[7,9)}", "{[2,8)}", "{[1,9)}"},
			{RangeIntersection, "{[1,3),[7,9)}", "{[2,8)}", "{[2,3),[7,8)}"},
			{RangeDifference, "{[1,9)}", "{[2,3),[4,5),[6,7)}", "{[1,2),[3,4),[5,6),[7,9)}"},
			{RangeDifference, "{[1,3),[7,9)}", "{[2,8)}", "{[1,2),[8,9)}"},
			{RangeDifference, "{[1,3),[7,9)}", "{(,)}", "{}"},
			{RangeDifference, "{(,)}", "{[1,3),[7,9)}", "{(,1),[3,7),[9,)}"},
		} {
			a, b := inspectUnitInput(t, d, tc.a), inspectUnitInput(t, d, tc.b)
			beforeA, beforeB := slices.Clone(a.multirange.members), slices.Clone(b.multirange.members)
			p := operatorUnitPlan(t, tc.op, d, d)
			got, err := p.Evaluate(ctx, [2]Value{a, b})
			if err != nil {
				t.Fatal(err)
			}
			inspectUnitOutput(t, got, tc.w)
			if !slices.Equal(a.multirange.members, beforeA) || !slices.Equal(b.multirange.members, beforeB) {
				t.Fatal("mutated published operands")
			}
			// A second execution may not overwrite a previously returned result buffer.
			retained := slices.Clone(got.multirange.members)
			if _, err := p.Evaluate(ctx, [2]Value{b, a}); err != nil {
				t.Fatal(err)
			}
			if !slices.Equal(retained, got.multirange.members) {
				t.Fatal("reused result storage")
			}
			if len(got.multirange.members) > 0 {
				got.multirange.members[0] = rangeDatum{flags: rangeEmpty}
				if !slices.Equal(a.multirange.members, beforeA) || !slices.Equal(b.multirange.members, beforeB) {
					t.Fatal("result aliases operand buffer")
				}
			}
		}
	}
}
func TestMultirangeOperatorOuterAdjacencyAndContainment(t *testing.T) {
	types := inspectUnitTypes()
	ctx := context.Background()
	d, r := types[Int4Multirange], types[Int4Range]
	m := inspectUnitInput(t, d, "{[1,3),[7,9)}")
	for _, tc := range []struct {
		op    RangeOperator
		input string
		want  bool
	}{
		{RangeAdjacent, "[3,7)", false}, {RangeAdjacent, "[9,10)", true},
		{RangeContains, "[2,8)", false}, {RangeOverlap, "[2,8)", true},
		{RangeContains, "empty", true},
	} {
		p := operatorUnitPlan(t, tc.op, d, r)
		got, err := p.Evaluate(ctx, [2]Value{m, inspectUnitInput(t, r, tc.input)})
		if err != nil || got.boolean != tc.want {
			t.Fatal(tc, got, err)
		}
	}
}
func TestMultirangeOperatorBoundedCancellation(t *testing.T) {
	types := inspectUnitTypes()
	d := types[Int4Multirange]
	makeMembers := func(offset int64) Value {
		members := make([]rangeDatum, 4096)
		for i := range members {
			lo := int64(i)*4 + offset
			members[i] = rangeDatum{lower: rangeBound{magnitude: lo}, upper: rangeBound{magnitude: lo + 1}, flags: rangeLowerInclusive}
		}
		return Value{typ: d, multirange: &multirangePayload{members}}
	}
	a, b := makeMembers(0), makeMembers(2)
	for _, op := range []RangeOperator{RangeContains, RangeOverlap, RangeUnion, RangeIntersection, RangeDifference} {
		args := [2]Value{a, b}
		if op == RangeContains {
			args[1] = a
		}
		p := operatorUnitPlan(t, op, d, d)
		c := &rangeSetCancelContext{Context: context.Background(), remaining: 3, failure: context.Canceled}
		if _, err := p.Evaluate(c, args); !errors.Is(err, context.Canceled) {
			t.Fatal("loop did not poll", op, err)
		}
	}
	cmp, err := BindComparison(d, d, CollationRef{}, false)
	if err != nil {
		t.Fatal(err)
	}
	c := &rangeSetCancelContext{Context: context.Background(), remaining: 3, failure: context.Canceled}
	if _, err := cmp.Compare(c, a, a, InputEnvironment{}); !errors.Is(err, context.Canceled) {
		t.Fatal("comparison did not poll", err)
	}
}
