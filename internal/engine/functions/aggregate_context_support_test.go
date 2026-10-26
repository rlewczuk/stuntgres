package functions

import (
	"context"
	"errors"
	"sort"
	"testing"

	"stuntgres/internal/sqlstate"
	"stuntgres/internal/sqltype"
)

func orderedSupportUnitRecipe(t *testing.T, final uint32, rows []sqltype.Usage, extra bool) *AggregateContextSupport {
	t.Helper()
	result, _ := aggregateUnitType(AggregateContextSupportResult(final))
	p, err := NewAggregateContextSupport(3971, final, extra, true, result.Usage(), rows, rows, []AggregateSortKey{{}})
	if err != nil { t.Fatal(err) }
	return p
}

func orderedSupportUnitOrdering(t *testing.T, usages []sqltype.Usage, desc, nullsFirst []bool) AggregateContextOrdering {
	t.Helper()
	comparisons := make([]sqltype.Comparison, len(usages))
	for i, usage := range usages {
		var err error
		comparisons[i], err = sqltype.BindComparison(usage.Type, usage.Type, usage.Collation, false)
		if err != nil { t.Fatal(err) }
	}
	return AggregateContextOrdering{
		Compare: func(ctx context.Context, left, right []sqltype.Value, env sqltype.InputEnvironment) (int, error) {
			for i, comparison := range comparisons {
				if err := ctx.Err(); err != nil { return 0, err }
				if left[i].IsNull() && right[i].IsNull() { continue }
				if left[i].IsNull() || right[i].IsNull() {
					if left[i].IsNull() == nullsFirst[i] { return -1, nil }
					return 1, nil
				}
				sign, err := comparison.Compare(ctx, left[i], right[i], env)
				if err != nil { return 0, err }
				if sign != 0 {
					if desc[i] { sign = -sign }
					return sign, nil
				}
			}
			return 0, ctx.Err()
		},
		Sort: func(ctx context.Context, indices []int, compare func(int, int) (int, error)) error {
			var failure error
			sort.SliceStable(indices, func(i, j int) bool {
				if failure != nil { return false }
				sign, err := compare(indices[i], indices[j])
				failure = err
				return sign < 0
			})
			if failure != nil { return failure }
			return ctx.Err()
		},
	}
}

func orderedSupportUnitValue(t *testing.T, typ *sqltype.Descriptor, text string) sqltype.Value {
	t.Helper()
	value, err := typ.Input(context.Background(), text, -1)
	if err != nil { t.Fatal(err) }
	return value
}

func orderedSupportUnitResult(t *testing.T, p *AggregateContextSupport, state any, target []sqltype.Value, order AggregateContextOrdering, expected string) {
	t.Helper()
	value, err := p.Finalize(context.Background(), state, target, sqltype.InputEnvironment{}, order)
	if err != nil { t.Fatal(err) }
	text, err := value.Type().Output(context.Background(), value)
	if err != nil || text != expected { t.Fatalf("result %q, error %v; want %q", text, err, expected) }
}

func TestAggregateOrderedSupportState(t *testing.T) {
	ctx := context.Background()
	integer, _ := aggregateUnitType(sqltype.Int4)
	rows := []sqltype.Usage{integer.Usage()}
	order := orderedSupportUnitOrdering(t, rows, []bool{false}, []bool{false})
	for _, tc := range []struct { oid uint32; full, empty string }{
		{3987, "2", "1"}, {3993, "2", "1"}, {3989, "0.25", "0"}, {3991, "0.8", "1"},
	} {
		p := orderedSupportUnitRecipe(t, tc.oid, rows, true)
		state := p.NewState()
		if state.(*orderedSupportState).values != nil { t.Fatal("eager tuple allocation") }
		target := []sqltype.Value{orderedSupportUnitValue(t, integer, "20")}
		orderedSupportUnitResult(t, p, state, target, AggregateContextOrdering{}, tc.empty)
		for _, input := range []string{"40", "20", "10", "20"} {
			borrowed := []sqltype.Value{orderedSupportUnitValue(t, integer, input)}
			if err := p.Advance(ctx, state, borrowed); err != nil { t.Fatal(err) }
			borrowed[0] = sqltype.Null(integer)
		}
		first, _ := integer.Output(ctx, state.(*orderedSupportState).values[0])
		if first != "40" { t.Fatal("SFUNC reordered rows or retained caller slice", first) }
		orderedSupportUnitResult(t, p, state, target, order, tc.full)
		// Final sorting uses indices, never reorders or overwrites transition data.
		first, _ = integer.Output(ctx, state.(*orderedSupportState).values[0])
		if first != "40" { t.Fatal("final overwrote state") }
		orderedSupportUnitResult(t, p, p.NewState(), target, AggregateContextOrdering{}, tc.empty)
		other := orderedSupportUnitRecipe(t, tc.oid, rows, true)
		if err := other.Advance(ctx, state, target); err == nil { t.Fatal("foreign recipe state accepted") }
	}
}

func TestAggregateOrderedSupportStateMultiKey(t *testing.T) {
	ctx := context.Background()
	integer, _ := aggregateUnitType(sqltype.Int4)
	text, _ := aggregateUnitType(sqltype.Text)
	rows := []sqltype.Usage{integer.Usage(), text.Usage()}
	target := []sqltype.Value{orderedSupportUnitValue(t, integer, "1"), sqltype.String(text, "b")}
	for _, tc := range []struct { oid uint32; asc, desc string }{
		{3987, "2", "1"}, {3993, "2", "1"}, {3989, "0.2", "0"}, {3991, "0.6666666666666666", "0.5"},
	} {
		p := orderedSupportUnitRecipe(t, tc.oid, rows, true)
		state := p.NewState()
		for _, pair := range [][2]string{{"2", "a"}, {"1", "b"}, {"NULL", "z"}, {"1", "a"}, {"1", "b"}} {
			first := sqltype.Null(integer)
			if pair[0] != "NULL" { first = orderedSupportUnitValue(t, integer, pair[0]) }
			if err := p.Advance(ctx, state, []sqltype.Value{first, sqltype.String(text, pair[1])}); err != nil { t.Fatal(err) }
		}
		orderedSupportUnitResult(t, p, state, target, orderedSupportUnitOrdering(t, rows, []bool{false, false}, []bool{false, false}), tc.asc)
		orderedSupportUnitResult(t, p, state, target, orderedSupportUnitOrdering(t, rows, []bool{false, true}, []bool{false, false}), tc.desc)
	}
	p := orderedSupportUnitRecipe(t, 3987, rows, true)
	state := p.NewState()
	if err := p.Advance(ctx, state, []sqltype.Value{sqltype.Null(integer), sqltype.String(text, "a")}); err != nil { t.Fatal(err) }
	if err := p.Advance(ctx, state, []sqltype.Value{orderedSupportUnitValue(t, integer, "1"), sqltype.String(text, "z")}); err != nil { t.Fatal(err) }
	nullTarget := []sqltype.Value{sqltype.Null(integer), sqltype.String(text, "a")}
	orderedSupportUnitResult(t, p, state, nullTarget, orderedSupportUnitOrdering(t, rows, []bool{false, false}, []bool{false, false}), "2")
	orderedSupportUnitResult(t, p, state, nullTarget, orderedSupportUnitOrdering(t, rows, []bool{false, false}, []bool{true, false}), "1")
}

func TestAggregateOrderedSupportStateCancellation(t *testing.T) {
	integer, _ := aggregateUnitType(sqltype.Int4)
	rows := []sqltype.Usage{integer.Usage()}
	p := orderedSupportUnitRecipe(t, 3987, rows, true)
	state := p.NewState()
	value := []sqltype.Value{orderedSupportUnitValue(t, integer, "1")}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if err := p.Advance(ctx, state, value); !errors.Is(err, context.Canceled) { t.Fatal(err) }
	if len(state.(*orderedSupportState).values) != 0 { t.Fatal("canceled forward published row") }
	if err := p.Advance(context.Background(), state, value); err != nil { t.Fatal(err) }
	if _, err := p.Finalize(ctx, state, value, sqltype.InputEnvironment{}, AggregateContextOrdering{}); !errors.Is(err, context.Canceled) { t.Fatal(err) }
	order := orderedSupportUnitOrdering(t, rows, []bool{false}, []bool{false})
	order.Sort = func(context.Context, []int, func(int, int) (int, error)) error { return context.Canceled }
	if _, err := p.Finalize(context.Background(), state, value, sqltype.InputEnvironment{}, order); !errors.Is(err, context.Canceled) { t.Fatal(err) }
	order = orderedSupportUnitOrdering(t, rows, []bool{false}, []bool{false})
	order.Compare = func(context.Context, []sqltype.Value, []sqltype.Value, sqltype.InputEnvironment) (int, error) { return 0, context.Canceled }
	if _, err := p.Finalize(context.Background(), state, value, sqltype.InputEnvironment{}, order); !errors.Is(err, context.Canceled) { t.Fatal(err) }
	orderedSupportUnitResult(t, p, state, value, orderedSupportUnitOrdering(t, rows, []bool{false}, []bool{false}), "1")
}

func TestAggregateOrderedSupportStateValidation(t *testing.T) {
	integer, _ := aggregateUnitType(sqltype.Int4)
	rows := []sqltype.Usage{integer.Usage()}
	p := orderedSupportUnitRecipe(t, 3987, rows, false)
	state := p.NewState()
	value := []sqltype.Value{orderedSupportUnitValue(t, integer, "1")}
	orderedSupportUnitResult(t, p, state, value, AggregateContextOrdering{}, "1")
	if err := p.Advance(context.Background(), state, nil); err == nil { t.Fatal("wrong width accepted") }
	if err := p.Advance(context.Background(), state, []sqltype.Value{{}}); err == nil { t.Fatal("untyped datum accepted") }
	if err := p.Advance(context.Background(), state, value); err != nil { t.Fatal(err) }
	_, err := p.Finalize(context.Background(), state, value, sqltype.InputEnvironment{}, AggregateContextOrdering{})
	aggregateUnitState(t, err, "XX000")
	if err.(*sqlstate.Error).Message != "wrong number of arguments in hypothetical-set function" { t.Fatal(err) }
	for _, oid := range []uint32{3971, 3987, 3989, 3991, 3993} {
		if !IsAggregateContextSupport(oid) { t.Fatal("missing support", oid) }
	}
	if IsAggregateContextSupport(3986) || AggregateContextSupportResult(3986) != 0 { t.Fatal("aggregate declaration accepted as support") }
}

func TestAggregateOrderedSupportStateContext(t *testing.T) {
	var support *AggregateContextSupport
	err := support.Advance(context.Background(), nil, nil)
	aggregateUnitState(t, err, "XX000")
	if err.(*sqlstate.Error).Message != "ordered-set aggregate called in non-aggregate context" {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if err := support.Advance(ctx, nil, nil); !errors.Is(err, context.Canceled) {
		t.Fatal("canceled native entrypoint did not preserve context error", err)
	}
}

func TestAggregateOrderedSupportStateCollation(t *testing.T) {
	text, _ := aggregateUnitType(sqltype.Text)
	collation, err := sqltype.RegisteredCollation(91000, 950)
	if err != nil { t.Fatal(err) }
	usage := text.Usage()
	usage.Collation = collation
	rows := []sqltype.Usage{usage}
	p := orderedSupportUnitRecipe(t, 3987, rows, true)
	rows[0] = sqltype.Usage{}
	if p.Rows()[0] != usage || p.Direct()[0] != usage { t.Fatal("recipe did not own full collated usages") }
	state := p.NewState()
	for _, value := range []string{"a", "Z"} {
		if err := p.Advance(context.Background(), state, []sqltype.Value{sqltype.String(text, value)}); err != nil { t.Fatal(err) }
	}
	orderedSupportUnitResult(t, p, state, []sqltype.Value{sqltype.String(text, "a")}, orderedSupportUnitOrdering(t, p.Rows(), []bool{false}, []bool{false}), "2")
	integer, _ := aggregateUnitType(sqltype.Int4)
	_, err = NewAggregateContextSupport(3971, 3986, true, true, integer.Usage(), p.Rows(), p.Direct(), p.Keys())
	if err == nil { t.Fatal("aggregate identity accepted instead of real support") }
	_, err = NewAggregateContextSupport(3971, 3987, true, true, integer.Usage(), p.Rows(), p.Direct(), p.Keys())
	if err == nil { t.Fatal("wrong selected final result type accepted") }
	nonhyp, err := NewAggregateContextSupport(p.TransitionOID(), p.FinalOID(), p.FinalExtra(), false, p.ResultUsage(), p.Rows(), p.Direct(), p.Keys())
	if err != nil { t.Fatal(err) }
	empty := nonhyp.NewState()
	target := []sqltype.Value{sqltype.String(text, "a")}
	orderedSupportUnitResult(t, nonhyp, empty, target, AggregateContextOrdering{}, "1")
	if err := nonhyp.Advance(context.Background(), empty, target); err != nil { t.Fatal(err) }
	_, err = nonhyp.Finalize(context.Background(), empty, target, sqltype.InputEnvironment{}, AggregateContextOrdering{})
	aggregateUnitState(t, err, "XX000")
	if err.(*sqlstate.Error).Message != "type mismatch in hypothetical-set function" { t.Fatal(err) }
}
