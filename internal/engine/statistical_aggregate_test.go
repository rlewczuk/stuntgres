package engine

import (
	"context"
	"stuntgres/internal/engine/functions"
	"stuntgres/internal/sqltype"
	"testing"
)

func TestStatisticalAggregateSlotsAndMetadata(t *testing.T) {
	_, q, source := queryCoreFixture(t, "SELECT var_pop(1),var_pop(1),var_samp(1),stddev_pop(1),stddev_samp(1),variance(1),stddev(1),regr_slope(2,1),regr_sxx(2,1),regr_sxy(2,1),regr_syy(2,1) FROM t")
	defer source.close()
	node := q.Root.(*projectNode).child.(*aggregateNode)
	if len(node.calls) != 8 {
		t.Fatal("final-operation sharing", len(node.calls))
	}
	for i, c := range q.Root.Schema() {
		want := sqltype.Numeric
		if i >= 7 {
			want = sqltype.Float8
		}
		if c.Usage.Type.ID() != want || c.Usage.Typmod != -1 || !c.Nullable || !c.NullabilityKnown || c.OriginTable != 0 || c.OriginAttribute != 0 {
			t.Fatal("moment metadata", i, c)
		}
	}
}

func TestStatisticalAggregateViewRemapping(t *testing.T) {
	source, destination := New(), New()
	ctx := context.Background()
	for _, regression := range []bool{false, true} {
		input, _ := source.CatalogRead().LookupType(sqltype.Numeric)
		result := input
		var provider aggregateProvider = functions.NewStatisticalAggregate(input, result.Usage(), true, true)
		if regression {
			input, _ = source.CatalogRead().LookupType(sqltype.Float8)
			result = input
			provider = functions.NewRegressionAggregate(input, result.Usage(), sqltype.RegressionSlope)
		}
		node := &aggregateNode{child: &singletonNode{}, calls: []aggregateCall{{provider: provider}}}
		mapped, err := remapViewQuery(destination.CatalogRead(), node, func(ref ObjectRef) ObjectRef { return ref }, func(old *sqltype.Descriptor) *sqltype.Descriptor {
			d, _ := destination.CatalogRead().LookupType(old.ID())
			return d
		})
		if err != nil {
			t.Fatal(err)
		}
		next := mapped.(*aggregateNode).calls[0].provider
		if next.ResultUsage().Type == provider.ResultUsage().Type {
			t.Fatal("foreign result retained")
		}
		state, err := next.Init(ctx)
		if err != nil {
			t.Fatal(err)
		}
		foreign, _ := input.Input(ctx, "1", -1)
		if err := next.Advance(ctx, state, foreign, foreign); err == nil {
			t.Fatal("foreign input retained")
		}
		d, _ := destination.CatalogRead().LookupType(input.ID())
		for _, text := range []string{"1", "3"} {
			v, _ := d.Input(ctx, text, -1)
			if err := next.Advance(ctx, state, v, v); err != nil {
				t.Fatal(err)
			}
		}
		v, err := next.Finalize(ctx, state)
		if err != nil || v.Type() != next.ResultUsage().Type || v.IsNull() {
			t.Fatal("remapped result", err)
		}
		text, err := v.Type().Output(ctx, v)
		want := "1.4142135623730950"
		if regression {
			want = "1"
		}
		if err != nil || text != want {
			t.Fatal(text, want, err)
		}
	}
}

func TestStatisticalAggregateEagerCancellation(t *testing.T) {
	for _, call := range []string{"var_pop(1::numeric)", "stddev_pop(1::numeric)", "var_samp(1::double precision)", "regr_sxx(2,1)"} {
		for _, phase := range []string{"advance", "finalize", "prepublication"} {
			_, q, source := queryCoreFixture(t, "SELECT "+call+" FROM t")
			project := q.Root.(*projectNode)
			node := project.child.(*aggregateNode)
			ctx, cancel := context.WithCancel(context.Background())
			original := node.calls[0].provider
			switch phase {
			case "advance":
				node.calls[0].provider = aggregateProbeProvider{aggregateProvider: original, advance: func(ctx context.Context, s any, v, x sqltype.Value) error {
					err := original.Advance(ctx, s, v, x)
					cancel()
					return err
				}}
			case "finalize":
				node.calls[0].provider = aggregateProbeProvider{aggregateProvider: original, finalize: func(ctx context.Context, s any) (sqltype.Value, error) {
					v, err := original.Finalize(ctx, s)
					cancel()
					return v, err
				}}
			case "prepublication":
				expr := project.expressions[0]
				project.expressions[0] = queryProbe(expr.ResultUsage(), func(c *EvalContext) (sqltype.Value, error) { v, err := expr.Eval(c); cancel(); return v, err })
			}
			rows, count, err := finalizeQuery(ctx, q.Root, source, nil)
			cancel()
			if rows != nil || count != 0 || err != context.Canceled || source.view != nil || source.anonymous != nil {
				t.Fatal(call, phase, rows, count, err)
			}
		}
	}
}
