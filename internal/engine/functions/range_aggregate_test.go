package functions

import (
	"context"
	"stuntgres/internal/sqltype"
	"testing"
)

func rangeAggregateUnitProvider(t *testing.T, d *sqltype.Descriptor, operation sqltype.RangeAggregateOperation) RangeAggregateProvider {
	t.Helper()
	bound, err := sqltype.BindRangeAggregate(d, operation)
	if err != nil {
		t.Fatal(err)
	}
	return RangeAggregateProvider{input: d.Usage(), usage: bound.ResultUsage(), operation: operation, bound: bound}
}

func TestRangeAggregateProviderState(t *testing.T) {
	ctx := context.Background()
	for _, id := range []sqltype.ID{sqltype.Int4Range, sqltype.Int8Range, sqltype.NumRange, sqltype.Int4Multirange, sqltype.Int8Multirange, sqltype.NumMultirange} {
		d, _ := aggregateUnitType(id)
		for _, operation := range []sqltype.RangeAggregateOperation{sqltype.RangeAggregateUnion, sqltype.RangeAggregateIntersection} {
			p := rangeAggregateUnitProvider(t, d, operation)
			s, err := p.Init(ctx)
			if err != nil {
				t.Fatal(err)
			}
			isolated, err := p.Init(ctx)
			if err != nil || s == isolated {
				t.Fatal("shared state", err)
			}
			text := "[1,4)"
			if id == sqltype.Int4Multirange || id == sqltype.Int8Multirange || id == sqltype.NumMultirange {
				text = "{[1,4)}"
			}
			v, err := d.Input(ctx, text, -1)
			if err != nil {
				t.Fatal(err)
			}
			if err = p.Advance(ctx, s, v, sqltype.Value{}); err != nil {
				t.Fatal(err)
			}
			result, err := p.Finalize(ctx, s)
			if err != nil || result.Type() != p.usage.Type || result.IsNull() {
				t.Fatal(result, err)
			}
			out, err := result.Type().Output(ctx, result)
			want := text
			if operation == sqltype.RangeAggregateUnion {
				want = "{[1,4)}"
			}
			if err != nil || out != want {
				t.Fatal(out, want, err)
			}
			result, err = p.Finalize(ctx, isolated)
			if err != nil || !result.IsNull() || result.Type() != p.usage.Type {
				t.Fatal("group leak", result, err)
			}
			if _, err = p.Finalize(ctx, &countState{}); err == nil {
				t.Fatal("foreign provider state accepted")
			}
		}
	}
}
