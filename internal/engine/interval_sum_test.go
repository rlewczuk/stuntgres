package engine

import (
	"context"
	"errors"
	"testing"

	"stuntgres/internal/engine/ids"
	"stuntgres/internal/sqlstate"
	"stuntgres/internal/sqltype"
)

func TestIntervalSumProvider(t *testing.T) {
	d, _ := New().CatalogRead().LookupType(sqltype.Interval)
	p := intervalSumProvider{d.Usage()}
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
	if err := p.Advance(ctx, state, sqltype.Null(d), sqltype.Value{}); err != nil {
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
		if err := p.Advance(ctx, state, v, sqltype.Value{}); err != nil {
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
	foreign, _ := New().CatalogRead().LookupType(sqltype.Interval)
	wrong, _ := sqltype.IntervalValue(foreign, 0, 1, 0)
	for _, invalid := range []sqltype.Value{{}, wrong, sqltype.Null(foreign)} {
		err := p.Advance(ctx, state, invalid, sqltype.Value{})
		var diagnostic *sqlstate.Error
		if err == nil || errors.As(err, &diagnostic) {
			t.Fatal("ownership error must be ordinary", err)
		}
	}
	for _, invalid := range []any{nil, struct{}{}, (*sqltype.IntervalAvgState)(nil), &moneySumState{}} {
		if err := p.Advance(ctx, invalid, sqltype.Null(d), sqltype.Value{}); err == nil {
			t.Fatal("invalid state accepted")
		}
		if _, err := p.Finalize(ctx, invalid); err == nil {
			t.Fatal("invalid final state accepted")
		}
	}
	money, _ := New().CatalogRead().LookupType(sqltype.Money)
	for _, invalid := range []intervalSumProvider{{}, {money.Usage()}} {
		if _, err := invalid.Init(ctx); err == nil {
			t.Fatal("invalid descriptor accepted")
		}
	}
}

func TestIntervalSumProviderCancellation(t *testing.T) {
	d, _ := New().CatalogRead().LookupType(sqltype.Interval)
	p := intervalSumProvider{d.Usage()}
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
		if err := p.Advance(ctx, invalid, v, sqltype.Value{}); err != context.Canceled {
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

func TestMoneySumProviderViewRemap(t *testing.T) { moneyIntervalSumProviderRemap(t, sqltype.Money) }
func TestIntervalSumProviderViewRemap(t *testing.T) {
	moneyIntervalSumProviderRemap(t, sqltype.Interval)
}

func moneyIntervalSumProviderRemap(t *testing.T, id sqltype.ID) {
	t.Helper()
	source, destination := New(), New()
	old, _ := source.CatalogRead().LookupType(id)
	owned, _ := destination.CatalogRead().LookupType(id)
	var p aggregateProvider
	operation := aggregateIntervalSum
	if id == sqltype.Money {
		p = moneySumUnitProvider(t, source)
		operation = aggregateMoneySum
	} else {
		p = intervalSumProvider{old.Usage()}
	}
	root := &aggregateNode{
		querySchema: querySchema{{Name: "sum", Usage: p.ResultUsage(), Nullable: true, NullabilityKnown: true}},
		child:       &valuesNode{},
		calls:       []aggregateCall{{provider: p, operation: operation}},
	}
	mapped, err := remapViewQuery(destination.CatalogRead(), root, func(ref ids.ObjectRef) ids.ObjectRef { return ref }, func(d *sqltype.Descriptor) *sqltype.Descriptor {
		if d == old {
			return owned
		}
		return d
	})
	if err != nil {
		t.Fatal(err)
	}
	call := mapped.(*aggregateNode).calls[0]
	provider := call.provider
	if provider.ResultUsage() != owned.Usage() || mapped.Schema()[0].Usage != owned.Usage() || call.operation != operation {
		t.Fatal("wrong remapped provider", provider.ResultUsage(), mapped.Schema())
	}
	if p.ResultUsage() != old.Usage() || root.Schema()[0].Usage != old.Usage() {
		t.Fatal("remap changed source provider")
	}
	ctx := context.Background()
	state, err := provider.Init(ctx)
	if err != nil {
		t.Fatal(err)
	}
	var input, foreign sqltype.Value
	if id == sqltype.Money {
		input, _ = sqltype.MoneyValue(owned, 1234)
		foreign, _ = sqltype.MoneyValue(old, 1234)
	} else {
		input, _ = sqltype.IntervalValue(owned, 1, 2, 9007199254740993)
		foreign, _ = sqltype.IntervalValue(old, 1, 2, 9007199254740993)
	}
	if err := provider.Advance(ctx, state, foreign, sqltype.Value{}); err == nil {
		t.Fatal("source descriptor accepted after remap")
	}
	for range 2 {
		if err := provider.Advance(ctx, state, input, sqltype.Value{}); err != nil {
			t.Fatal("remapped bound operator", err)
		}
	}
	out, err := provider.Finalize(ctx, state)
	if err != nil || out.Type() != owned {
		t.Fatal(out, err)
	}
	if id == sqltype.Money {
		n, err := out.MoneyUnits()
		if err != nil || n != 2468 {
			t.Fatal(n, err)
		}
	} else {
		m, days, u, err := out.Interval()
		if err != nil || m != 2 || days != 4 || u != 18014398509481986 {
			t.Fatal(m, days, u, err)
		}
	}
}
