package engine

import (
	"context"
	"stuntgres/internal/engine/functions"
	"testing"

	"stuntgres/internal/engine/ids"
	"stuntgres/internal/sqltype"
)

func TestMoneySumProviderViewRemap(t *testing.T) { moneyIntervalSumProviderRemap(t, sqltype.Money) }
func TestIntervalSumProviderViewRemap(t *testing.T) {
	moneyIntervalSumProviderRemap(t, sqltype.Interval)
}

func moneyIntervalSumProviderRemap(t *testing.T, id sqltype.ID) {
	t.Helper()
	source, destination := New(), New()
	old, _ := source.CatalogRead().LookupType(id)
	owned, _ := destination.CatalogRead().LookupType(id)
	var p functions.AggregateProvider
	operation := functions.AggregateIntervalSum
	if id == sqltype.Money {
		bound, err := sqltype.BindArithmetic(sqltype.ArithmeticAdd, old, old, old)
		if err != nil {
			t.Fatal(err)
		}
		p = functions.NewMoneySum(old.Usage(), bound)
		operation = functions.AggregateMoneySum
	} else {
		p = functions.NewIntervalSum(old.Usage())
	}
	root := &aggregateNode{
		QuerySchema: querySchema{{Name: "sum", Usage: p.ResultUsage(), Nullable: true, NullabilityKnown: true}},
		Child:       &valuesNode{},
		Calls:       []aggregateCall{{Provider: p, Operation: operation}},
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
	call := mapped.(*aggregateNode).Calls[0]
	provider := call.Provider
	if provider.ResultUsage() != owned.Usage() || mapped.Schema()[0].Usage != owned.Usage() || call.Operation != operation {
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
