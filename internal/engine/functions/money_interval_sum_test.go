package functions

import (
	"context"
	"errors"
	"math"
	"reflect"
	"testing"

	"stuntgres/internal/sqlstate"
	"stuntgres/internal/sqltype"
)

// builtinUnitType finds a descriptor in one builtin generation; separate
// generations stand for separate engines' catalogs.
func builtinUnitType(types []*sqltype.Descriptor, id sqltype.ID) (*sqltype.Descriptor, bool) {
	for _, d := range types {
		if d.ID() == id {
			return d, true
		}
	}
	return nil, false
}

func moneySumUnitProvider(t *testing.T, types []*sqltype.Descriptor) MoneySum {
	t.Helper()
	d, ok := builtinUnitType(types, sqltype.Money)
	if !ok {
		t.Fatal("missing money descriptor")
	}
	bound, err := sqltype.BindArithmetic(sqltype.ArithmeticAdd, d, d, d)
	if err != nil {
		t.Fatal(err)
	}
	return NewMoneySum(d.Usage(), bound)
}

func TestMoneySumProvider(t *testing.T) {
	p := moneySumUnitProvider(t, sqltype.Builtins())
	ctx := context.Background()
	if p.ResultUsage().Typmod != -1 || p.ResultUsage().Type.ID() != sqltype.Money {
		t.Fatal(p.ResultUsage())
	}
	for _, c := range []struct {
		inputs []int64
		want   int64
	}{
		{[]int64{1234, 5678}, 6912},
		{[]int64{9007199254740993, 1}, 9007199254740994},
		{[]int64{math.MaxInt64, math.MinInt64}, -1},
		{[]int64{100, -100}, 0},
		{[]int64{math.MaxInt64}, math.MaxInt64},
		{[]int64{math.MinInt64}, math.MinInt64},
	} {
		state, err := p.Init(ctx)
		if err != nil {
			t.Fatal(err)
		}
		isolated, err := p.Init(ctx)
		if err != nil {
			t.Fatal(err)
		}
		if err := p.Advance(ctx, state, []sqltype.Value{sqltype.Null(p.usage.Type), sqltype.Value{}}, nil); err != nil {
			t.Fatal(err)
		}
		empty, err := p.Finalize(ctx, state)
		if err != nil || !empty.IsNull() || empty.Type() != p.usage.Type {
			t.Fatal(empty, err)
		}
		for _, units := range c.inputs {
			v, err := sqltype.MoneyValue(p.usage.Type, units)
			if err != nil {
				t.Fatal(err)
			}
			if err := p.Advance(ctx, state, []sqltype.Value{v, sqltype.Value{}}, nil); err != nil {
				t.Fatal(err)
			}
		}
		before := *state.(*moneySumState)
		for range 2 {
			out, err := p.Finalize(ctx, state)
			if err != nil || out.Type() != p.usage.Type || out.IsNull() {
				t.Fatal(out, err)
			}
			units, err := out.MoneyUnits()
			if err != nil || units != c.want {
				t.Fatal(units, err, c.want)
			}
		}
		if !reflect.DeepEqual(before, *state.(*moneySumState)) {
			t.Fatal("finalization changed state")
		}
		out, err := p.Finalize(ctx, isolated)
		if err != nil || !out.IsNull() || out.Type() != p.usage.Type {
			t.Fatal("group leaked", out, err)
		}
	}
	for _, c := range []struct{ first, next int64 }{{math.MaxInt64, 1}, {math.MinInt64, -1}} {
		state, err := p.Init(ctx)
		if err != nil {
			t.Fatal(err)
		}
		v, _ := sqltype.MoneyValue(p.usage.Type, c.first)
		if err := p.Advance(ctx, state, []sqltype.Value{v, sqltype.Value{}}, nil); err != nil {
			t.Fatal(err)
		}
		before := *state.(*moneySumState)
		v, _ = sqltype.MoneyValue(p.usage.Type, c.next)
		err = p.Advance(ctx, state, []sqltype.Value{v, sqltype.Value{}}, nil)
		var diagnostic *sqlstate.Error
		if !errors.As(err, &diagnostic) || diagnostic.Code != "22003" || diagnostic.Message != "money out of range" || diagnostic.Position != "" || diagnostic.Hint != "" || diagnostic.Context != "" || diagnostic.Detail != "" {
			t.Fatal(err)
		}
		if !reflect.DeepEqual(before, *state.(*moneySumState)) {
			t.Fatal("failed transition changed state")
		}
		out, err := p.Finalize(ctx, state)
		if err != nil {
			t.Fatal(err)
		}
		units, err := out.MoneyUnits()
		if err != nil || units != c.first {
			t.Fatal("failed transition changed result", units, err)
		}
	}
	state, err := p.Init(ctx)
	if err != nil {
		t.Fatal(err)
	}
	foreign := moneySumUnitProvider(t, sqltype.Builtins())
	wrong, _ := sqltype.MoneyValue(foreign.usage.Type, 1)
	for _, invalid := range []sqltype.Value{{}, wrong, sqltype.Null(foreign.usage.Type)} {
		err := p.Advance(ctx, state, []sqltype.Value{invalid, sqltype.Value{}}, nil)
		var diagnostic *sqlstate.Error
		if err == nil || errors.As(err, &diagnostic) {
			t.Fatal("ownership error must be ordinary", err)
		}
	}
	for _, invalid := range []any{nil, struct{}{}, (*moneySumState)(nil), &sqltype.IntervalAvgState{}} {
		if err := p.Advance(ctx, invalid, []sqltype.Value{sqltype.Null(p.usage.Type), sqltype.Value{}}, nil); err == nil {
			t.Fatal("invalid state accepted")
		}
		if _, err := p.Finalize(ctx, invalid); err == nil {
			t.Fatal("invalid final state accepted")
		}
	}
	for _, invalid := range []MoneySum{{}, {usage: p.usage}} {
		if _, err := invalid.Init(ctx); err == nil {
			t.Fatal("invalid descriptor/binding accepted")
		}
	}
}

func TestMoneySumProviderCancellation(t *testing.T) {
	p := moneySumUnitProvider(t, sqltype.Builtins())
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if state, err := p.Init(ctx); state != nil || err != context.Canceled {
		t.Fatal(state, err)
	}
	state, err := p.Init(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	v, _ := sqltype.MoneyValue(p.usage.Type, 1234)
	for _, invalid := range []any{state, nil} {
		if err := p.Advance(ctx, invalid, []sqltype.Value{v, sqltype.Value{}}, nil); err != context.Canceled {
			t.Fatal(err)
		}
		if out, err := p.Finalize(ctx, invalid); err != context.Canceled || out.Type() != nil {
			t.Fatal(out, err)
		}
	}
	out, err := p.Finalize(context.Background(), state)
	if err != nil || !out.IsNull() {
		t.Fatal("canceled transition changed state", out, err)
	}
}

func TestIntervalSumProvider(t *testing.T) {
	d, _ := builtinUnitType(sqltype.Builtins(), sqltype.Interval)
	p := IntervalSum{d.Usage()}
	ctx := context.Background()
	state, err := p.Init(ctx)
	if err != nil {
		t.Fatal(err)
	}
	isolated, err := p.Init(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if p.ResultUsage() != d.Usage() || p.ResultUsage().Typmod != -1 {
		t.Fatal(p.ResultUsage())
	}
	if err := p.Advance(ctx, state, []sqltype.Value{sqltype.Null(d), sqltype.Value{}}, nil); err != nil {
		t.Fatal(err)
	}
	empty, err := p.Finalize(ctx, state)
	if err != nil || !empty.IsNull() || empty.Type() != d {
		t.Fatal(empty, err)
	}
	for _, in := range []struct {
		m, days int32
		u       int64
	}{{1, 0, 9007199254740993}, {0, 30, 1}} {
		v, err := sqltype.IntervalValue(d, in.m, in.days, in.u)
		if err != nil {
			t.Fatal(err)
		}
		if err := p.Advance(ctx, state, []sqltype.Value{v, sqltype.Value{}}, nil); err != nil {
			t.Fatal(err)
		}
	}
	before := *state.(*sqltype.IntervalAvgState)
	for range 2 {
		out, err := p.Finalize(ctx, state)
		if err != nil || out.Type() != d {
			t.Fatal(out, err)
		}
		m, days, u, err := out.Interval()
		if err != nil || m != 1 || days != 30 || u != 9007199254740994 {
			t.Fatal(m, days, u, err)
		}
	}
	if before != *state.(*sqltype.IntervalAvgState) {
		t.Fatal("finalization changed state")
	}
	out, err := p.Finalize(ctx, isolated)
	if err != nil || !out.IsNull() || out.Type() != d {
		t.Fatal("group leaked", out, err)
	}
	foreign, _ := builtinUnitType(sqltype.Builtins(), sqltype.Interval)
	wrong, _ := sqltype.IntervalValue(foreign, 0, 1, 0)
	for _, invalid := range []sqltype.Value{{}, wrong, sqltype.Null(foreign)} {
		err := p.Advance(ctx, state, []sqltype.Value{invalid, sqltype.Value{}}, nil)
		var diagnostic *sqlstate.Error
		if err == nil || errors.As(err, &diagnostic) {
			t.Fatal("ownership error must be ordinary", err)
		}
	}
	for _, invalid := range []any{nil, struct{}{}, (*sqltype.IntervalAvgState)(nil), &moneySumState{}} {
		if err := p.Advance(ctx, invalid, []sqltype.Value{sqltype.Null(d), sqltype.Value{}}, nil); err == nil {
			t.Fatal("invalid state accepted")
		}
		if _, err := p.Finalize(ctx, invalid); err == nil {
			t.Fatal("invalid final state accepted")
		}
	}
	money, _ := builtinUnitType(sqltype.Builtins(), sqltype.Money)
	for _, invalid := range []IntervalSum{{}, {money.Usage()}} {
		if _, err := invalid.Init(ctx); err == nil {
			t.Fatal("invalid descriptor accepted")
		}
	}
}

func TestIntervalSumProviderCancellation(t *testing.T) {
	d, _ := builtinUnitType(sqltype.Builtins(), sqltype.Interval)
	p := IntervalSum{d.Usage()}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if state, err := p.Init(ctx); state != nil || err != context.Canceled {
		t.Fatal(state, err)
	}
	state, err := p.Init(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	v, _ := sqltype.IntervalValue(d, 1, 2, 3)
	for _, invalid := range []any{state, nil} {
		if err := p.Advance(ctx, invalid, []sqltype.Value{v, sqltype.Value{}}, nil); err != context.Canceled {
			t.Fatal(err)
		}
		if out, err := p.Finalize(ctx, invalid); err != context.Canceled || out.Type() != nil {
			t.Fatal(out, err)
		}
	}
	out, err := p.Finalize(context.Background(), state)
	if err != nil || !out.IsNull() {
		t.Fatal("canceled transition changed state", out, err)
	}
}
