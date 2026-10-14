package functions

import (
	"context"
	"errors"
	"reflect"
	"stuntgres/internal/sqltype"
	"testing"
)

func inspectCarrierTypes() map[sqltype.ID]*sqltype.Descriptor {
	m := map[sqltype.ID]*sqltype.Descriptor{}
	for _, d := range sqltype.Builtins() {
		m[d.ID()] = d
	}
	return m
}
func TestRangeInspectCarrierDemand(t *testing.T) {
	types := inspectCarrierTypes()
	d := types[sqltype.Int4Range]
	var order []int
	first := stringUnitArgument{Info: Info[struct{}]{Usage: d.Usage()}, value: sqltype.Null(d), order: &order, index: 0}
	second := stringUnitArgument{Info: Info[struct{}]{Usage: d.Usage()}, value: sqltype.Null(d), order: &order, index: 1}
	node, err := NewRangeInspect[stringUnitExpression, *stringUnitContext](Info[struct{}]{Usage: d.Usage()}, sqltype.RangeMerge, []stringUnitExpression{first, second})
	if err != nil {
		t.Fatal(err)
	}
	c := &stringUnitContext{Context: context.Background()}
	value, err := node.Eval(c)
	if err != nil || !value.IsNull() || value.Type() != d || !reflect.DeepEqual(order, []int{0, 1}) {
		t.Fatal(value, order, err)
	}
	failure := errors.New("range inspection demanded sibling")
	second.err = failure
	node.Args[1] = second
	order = nil
	if _, err := node.Eval(c); !errors.Is(err, failure) || !reflect.DeepEqual(order, []int{0, 1}) {
		t.Fatal("runtime NULL suppressed sibling", order, err)
	}
	first.literal = true
	node.Args[0] = first
	order = nil
	value, err = node.Eval(c)
	if err != nil || !value.IsNull() || len(order) != 0 {
		t.Fatal("literal NULL demand", value, order, err)
	}
	canceled, cancel := context.WithCancel(c.Context)
	cancel()
	order = nil
	if _, err := node.Eval(&stringUnitContext{Context: canceled}); !errors.Is(err, context.Canceled) || len(order) != 0 {
		t.Fatal("cancel before demand", order, err)
	}
	if err := node.Validate(); err != nil {
		t.Fatal(err)
	}
}
func TestRangeInspectCarrierReconstruct(t *testing.T) {
	source, target := inspectCarrierTypes(), inspectCarrierTypes()
	var order []int
	arg := func(d *sqltype.Descriptor) stringUnitExpression {
		return stringUnitArgument{Info: Info[struct{}]{Usage: d.Usage()}, value: sqltype.Null(d), order: &order}
	}
	for _, op := range []sqltype.RangeInspectOperation{sqltype.RangeLower, sqltype.RangeUpper, sqltype.RangeIsEmpty, sqltype.RangeMerge} {
		oldInput, newInput := source[sqltype.Int4Multirange], target[sqltype.Int4Multirange]
		oldResult, newResult := sqltype.RangeInspectResult(op, oldInput), sqltype.RangeInspectResult(op, newInput)
		original, err := NewRangeInspect[stringUnitExpression, *stringUnitContext](Info[struct{}]{Usage: oldResult.Usage()}, op, []stringUnitExpression{arg(oldInput)})
		if err != nil {
			t.Fatal(err)
		}
		next, err := original.Reconstruct(Info[struct{}]{Usage: newResult.Usage()}, []stringUnitExpression{arg(newInput)})
		if err != nil || next.Validate() != nil {
			t.Fatal(err)
		}
		value, err := next.Eval(&stringUnitContext{Context: context.Background()})
		if err != nil || value.Type() != newResult {
			t.Fatal("fresh result", value, err)
		}
		if original.Usage.Type != oldResult || original.Args[0].ResultUsage().Type != oldInput {
			t.Fatal("mutated source carrier")
		}
		if _, err := original.Reconstruct(Info[struct{}]{Usage: oldResult.Usage()}, []stringUnitExpression{arg(newInput)}); err == nil {
			t.Fatal("foreign result authority")
		}
		forged := next
		forged.Plan = original.Plan
		if forged.Validate() == nil {
			t.Fatal("stale plan accepted")
		}
	}
	if _, err := NewRangeInspect[stringUnitExpression, *stringUnitContext](Info[struct{}]{Usage: source[sqltype.Int4Range].Usage()}, sqltype.RangeMerge, nil); err == nil {
		t.Fatal("invalid arity")
	}
	if RangeInspectIntrinsicRule() != ExactImmutable {
		t.Fatal("not immutable")
	}
}
func TestRangeInspectSelection(t *testing.T) {
	types := inspectCarrierTypes()
	for _, tc := range []struct {
		op   sqltype.RangeInspectOperation
		ids  []sqltype.ID
		want Resolution
	}{
		{sqltype.RangeLower, []sqltype.ID{sqltype.Int4Range}, Resolved}, {sqltype.RangeLower, []sqltype.ID{sqltype.DateRange}, Resolved},
		{sqltype.RangeIsEmpty, []sqltype.ID{sqltype.NumMultirange}, Resolved}, {sqltype.RangeIsEmpty, []sqltype.ID{0}, Ambiguous},
		{sqltype.RangeMerge, []sqltype.ID{0}, UnknownPolymorphic}, {sqltype.RangeMerge, []sqltype.ID{0, 0}, UnknownPolymorphic},
		{sqltype.RangeMerge, []sqltype.ID{sqltype.Int4Range, 0}, Resolved}, {sqltype.RangeMerge, []sqltype.ID{0, sqltype.Int4Range}, Resolved},
		{sqltype.RangeMerge, []sqltype.ID{sqltype.Int4Range, sqltype.Int8Range}, Missing},
		{sqltype.RangeMerge, []sqltype.ID{sqltype.Int4Range}, Missing}, {sqltype.RangeMerge, []sqltype.ID{sqltype.Int4Multirange}, Resolved},
		{sqltype.RangeMerge, []sqltype.ID{sqltype.Int4Multirange, sqltype.Int4Multirange}, Missing}, {sqltype.RangeIsEmpty, []sqltype.ID{sqltype.Int4}, Missing},
	} {
		args := make([]Argument[struct{}], len(tc.ids))
		for i, id := range tc.ids {
			if id != 0 {
				args[i].Usage = types[id].Usage()
			}
		}
		selected, got := ResolveRangeInspect(tc.op, true, args)
		if got != tc.want || got == Resolved && selected == nil {
			t.Fatal(tc, selected, got)
		}
		if _, got := ResolveRangeInspect(tc.op, false, args); got != Missing {
			t.Fatal("invisible overload", got)
		}
	}
}
