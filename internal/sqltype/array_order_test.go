package sqltype

import (
	"context"
	"errors"
	"testing"

	"stuntgres/internal/sqlstate"
)

func arrayOrderInput(t *testing.T, d *Descriptor, text string) Value {
	t.Helper()
	v, err := d.Input(context.Background(), text, -1)
	if err != nil {
		t.Fatal(err)
	}
	return v
}

func TestArrayOrderProvider(t *testing.T) {
	types := testTypes()
	ctx := context.Background()
	d := types[1007]
	p, err := BindComparison(d, d, CollationRef{}, false)
	if err != nil {
		t.Fatal(err)
	}
	for _, tc := range []struct {
		a, b string
		sign int
	}{
		{"{1,9}", "{2,0}", -1}, {"{2}", "{1,99}", 1}, {"{1}", "{1,0}", -1},
		{"{{1,2},{3,4}}", "{{1,2},{3,5}}", -1}, {"{2,0,0,0}", "{{1,9},{9,9}}", 1},
		{"[0:1]={1,2}", "{1,2}", -1}, {"{1,2,3,4}", "{{1,2},{3,4}}", -1},
		{"[9:10][9:11]={{1,2,3},{4,5,6}}", "[0:2][0:1]={{1,2},{3,4},{5,6}}", -1},
		{"[9:10][1:2][1:3]={{{1,2,3},{4,5,6}},{{7,8,9},{10,11,12}}}", "[0:1][1:3][1:2]={{{1,2},{3,4},{5,6}},{{7,8},{9,10},{11,12}}}", -1},
		{"{NULL}", "{NULL}", 0}, {"{NULL}", "{1}", 1}, {"{1,NULL}", "{1,2}", 1}, {"{}", "{}", 0}, {"{}", "{NULL}", -1},
	} {
		t.Run(tc.a+"/"+tc.b, func(t *testing.T) {
			a, b := arrayOrderInput(t, d, tc.a), arrayOrderInput(t, d, tc.b)
			if sign, err := p.Compare(ctx, a, b, InputEnvironment{}); err != nil || sign != tc.sign {
				t.Fatal(sign, err)
			}
			if sign, err := p.Compare(ctx, b, a, InputEnvironment{}); err != nil || sign != -tc.sign {
				t.Fatal(sign, err)
			}
			if equal, err := p.EqualValues(ctx, a, b, InputEnvironment{}); err != nil || equal != (tc.sign == 0) {
				t.Fatal(equal, err)
			}
			for op, want := range []bool{tc.sign == 0, tc.sign != 0, tc.sign < 0, tc.sign <= 0, tc.sign > 0, tc.sign >= 0} {
				predicate, err := BindComparisonPredicate(ComparisonOperator(op), d, d, types[Bool], CollationRef{}, false)
				if err != nil {
					t.Fatal(err)
				}
				got, err := predicate.Evaluate(ctx, a, b, InputEnvironment{})
				if err != nil {
					t.Fatal(err)
				}
				truth, err := got.Boolean()
				if err != nil || truth != want {
					t.Fatal(truth, err)
				}
				got, err = predicate.Evaluate(ctx, Null(d), b, InputEnvironment{})
				if err != nil || !got.IsNull() || got.Type() != types[Bool] {
					t.Fatal(got, err)
				}
			}
		})
	}
	a := arrayOrderInput(t, d, "{1,2,NULL}")
	if n := testing.AllocsPerRun(100, func() {
		if _, err := p.Compare(ctx, a, a, InputEnvironment{}); err != nil {
			panic(err)
		}
		if _, err := p.EqualValues(ctx, a, a, InputEnvironment{}); err != nil {
			panic(err)
		}
	}); n != 0 {
		t.Fatal("array evaluation allocations", n)
	}
	foreign := testTypes()[1007]
	v := arrayOrderInput(t, foreign, "{1,2,NULL}")
	if _, err := p.Compare(ctx, a, v, InputEnvironment{}); err == nil {
		t.Fatal("foreign generation compared")
	}
	if _, err := p.EqualValues(ctx, a, v, InputEnvironment{}); err == nil {
		t.Fatal("foreign generation equated")
	}
	if _, err := BindComparison(d, foreign, CollationRef{}, false); err == nil {
		t.Fatal("foreign array signature bound")
	}
	if _, err := BindComparison(d, types[1016], CollationRef{}, false); err == nil {
		t.Fatal("cross-width array signature bound")
	}
	if _, err := BindOrdering(d.Usage()); err == nil {
		t.Fatal("array-key ordering expanded")
	}
}

func TestArrayOrderDemand(t *testing.T) {
	types := testTypes()
	ctx := context.Background()
	json := types[199]
	p, err := BindComparison(json, json, CollationRef{}, false)
	if err != nil {
		t.Fatal(err)
	}
	assertMissing := func(err error, message string) {
		t.Helper()
		var d *sqlstate.Error
		if !errors.As(err, &d) || d.Code != "42883" || d.Message != message {
			t.Fatal(err)
		}
	}
	for _, text := range []string{"{}", "{NULL}", `{"{}"}`} {
		a := arrayOrderInput(t, json, text)
		_, err := p.EqualValues(ctx, a, a, InputEnvironment{})
		assertMissing(err, "could not identify an equality operator for type json")
		_, err = p.Compare(ctx, a, a, InputEnvironment{})
		assertMissing(err, "could not identify a comparison function for type json")
	}
	empty, nul := arrayOrderInput(t, json, "{}"), arrayOrderInput(t, json, "{NULL}")
	if equal, err := p.EqualValues(ctx, empty, nul, InputEnvironment{}); err != nil || equal {
		t.Fatal(equal, err)
	}
	text := types[1009]
	conflict, err := BindComparison(text, text, text.Usage().Collation, true)
	if err != nil {
		t.Fatal(err)
	}
	for _, pair := range [][2]string{{"{}", "{}"}, {"{NULL}", "{NULL}"}, {"{}", "{a}"}, {"{NULL}", "{a}"}} {
		a, b := arrayOrderInput(t, text, pair[0]), arrayOrderInput(t, text, pair[1])
		if _, err := conflict.EqualValues(ctx, a, b, InputEnvironment{}); err != nil {
			t.Fatal("premature equality collation demand", err)
		}
		if _, err := conflict.Compare(ctx, a, b, InputEnvironment{}); err != nil {
			t.Fatal("premature ordering collation demand", err)
		}
	}
	a := arrayOrderInput(t, text, "{a}")
	for _, equal := range []bool{false, true} {
		var err error
		if equal {
			_, err = conflict.EqualValues(ctx, a, a, InputEnvironment{})
		} else {
			_, err = conflict.Compare(ctx, a, a, InputEnvironment{})
		}
		var d *sqlstate.Error
		if !errors.As(err, &d) || d.Code != "42P22" {
			t.Fatal("missing reached collation demand", err)
		}
	}
}

func TestArrayOrderPlanEquality(t *testing.T) {
	types := testTypes()
	array := types[1007]
	a, err := BindComparison(array, array, CollationRef{}, false)
	if err != nil {
		t.Fatal(err)
	}
	b, err := BindComparison(array, array, CollationRef{}, false)
	if err != nil {
		t.Fatal(err)
	}
	if a.element == b.element || !a.Equal(b) {
		t.Fatal("rebound plans use child pointer identity")
	}
	b.element.missingEquality = true
	if a.Equal(b) {
		t.Fatal("missing capability omitted")
	}
	fresh := testTypes()[1007]
	other, err := BindComparison(fresh, fresh, CollationRef{}, false)
	if err != nil {
		t.Fatal(err)
	}
	if a.Equal(other) {
		t.Fatal("foreign descriptor generation equated")
	}
	eq, err := BindComparisonPredicate(CompareEqual, array, array, types[Bool], CollationRef{}, false)
	if err != nil {
		t.Fatal(err)
	}
	same, err := BindComparisonPredicate(CompareEqual, array, array, types[Bool], CollationRef{}, false)
	if err != nil {
		t.Fatal(err)
	}
	ne, err := BindComparisonPredicate(CompareNotEqual, array, array, types[Bool], CollationRef{}, false)
	if err != nil {
		t.Fatal(err)
	}
	if !eq.Equal(same) || eq.Equal(ne) {
		t.Fatal("predicate structural identity")
	}
	text := types[1009]
	one, err := BindComparison(text, text, text.Usage().Collation, false)
	if err != nil {
		t.Fatal(err)
	}
	two, err := BindComparison(text, text, text.Usage().Collation, true)
	if err != nil {
		t.Fatal(err)
	}
	if one.Equal(two) {
		t.Fatal("collation conflict omitted")
	}
}

// Err calls cancel deterministically inside provider loops, without scheduler,
// clocks, enormous fixtures, or production test hooks.
type arrayOrderCancelContext struct {
	context.Context
	remaining int
}

func (c *arrayOrderCancelContext) Err() error {
	c.remaining--
	if c.remaining <= 0 {
		return context.Canceled
	}
	return nil
}

func TestArrayOrderCancellation(t *testing.T) {
	d := testTypes()[1007]
	p, err := BindComparison(d, d, CollationRef{}, false)
	if err != nil {
		t.Fatal(err)
	}
	a := arrayOrderInput(t, d, "{1,2,3,4,5,6,7,8}")
	for _, remaining := range []int{1, 2, 8} {
		ctx := &arrayOrderCancelContext{Context: context.Background(), remaining: remaining}
		if _, err := p.EqualValues(ctx, a, a, InputEnvironment{}); !errors.Is(err, context.Canceled) {
			t.Fatal("equality cancellation", remaining, err)
		}
		ctx = &arrayOrderCancelContext{Context: context.Background(), remaining: remaining}
		if _, err := p.Compare(ctx, a, a, InputEnvironment{}); !errors.Is(err, context.Canceled) {
			t.Fatal("ordering cancellation", remaining, err)
		}
	}
	dims := make([]ArrayDimension, 6)
	for i := range dims {
		dims[i] = ArrayDimension{Lower: 1, Length: 1}
	}
	shaped, err := OwnedArrayValue(d, dims, []Value{Null(d.Element())})
	if err != nil {
		t.Fatal(err)
	}
	ctx := &arrayOrderCancelContext{Context: context.Background(), remaining: 4}
	if _, err := p.Compare(ctx, shaped, shaped, InputEnvironment{}); !errors.Is(err, context.Canceled) {
		t.Fatal("dimension ordering cancellation", err)
	}
	ctx = &arrayOrderCancelContext{Context: context.Background(), remaining: 3}
	if _, err := p.EqualValues(ctx, shaped, shaped, InputEnvironment{}); !errors.Is(err, context.Canceled) {
		t.Fatal("dimension equality cancellation", err)
	}
}
