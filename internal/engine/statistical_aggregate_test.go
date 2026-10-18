package engine

import (
	"context"
	"stuntgres/internal/engine/functions"
	"stuntgres/internal/engine/ids"
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
	_, selected, selectedSource := queryCoreFixture(t, "SELECT corr(2,1),covar_pop(2,1),covar_samp(2,1),regr_avgx(2,1),regr_avgy(2,1),regr_count(2,1),regr_intercept(2,1),regr_r2(2,1) FROM t")
	defer selectedSource.close()
	for i, c := range selected.Root.Schema() {
		want := sqltype.Float8
		if i == 5 {
			want = sqltype.Int8
		}
		if c.Usage.Type.ID() != want || c.Usage.Typmod != -1 || c.Nullable != (i != 5) || !c.NullabilityKnown || c.OriginTable != 0 || c.OriginAttribute != 0 {
			t.Fatal("paired statistic metadata", i, c)
		}
	}
}

func TestStatisticalAggregateViewRemapping(t *testing.T) {
	source, destination := New(), New()
	ctx := context.Background()
	for _, final := range []int{-1, int(sqltype.RegressionSlope), int(sqltype.RegressionSxx), int(sqltype.RegressionSxy), int(sqltype.RegressionSyy),
		int(sqltype.RegressionCorr), int(sqltype.RegressionCovarPop), int(sqltype.RegressionCovarSamp), int(sqltype.RegressionAvgx),
		int(sqltype.RegressionAvgy), int(sqltype.RegressionIntercept), int(sqltype.RegressionR2), -2} {
		input, _ := source.CatalogRead().LookupType(sqltype.Numeric)
		result := input
		var provider aggregateProvider = functions.NewStatisticalAggregate(input, result.Usage(), true, true)
		if final >= 0 {
			input, _ = source.CatalogRead().LookupType(sqltype.Float8)
			result = input
			provider = functions.NewRegressionAggregate(input, result.Usage(), sqltype.RegressionOperation(final))
		} else if final == -2 {
			input, _ = source.CatalogRead().LookupType(sqltype.Float8)
			result, _ = source.CatalogRead().LookupType(sqltype.Int8)
			provider = functions.NewRegressionCountAggregate(input, result.Usage())
		}
		node := &aggregateNode{child: &singletonNode{}, calls: []aggregateCall{{provider: provider}}}
		mapped, err := remapViewQuery(destination.CatalogRead(), node, func(ref ids.ObjectRef) ids.ObjectRef { return ref }, func(old *sqltype.Descriptor) *sqltype.Descriptor {
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
		if final >= 0 {
			want = map[sqltype.RegressionOperation]string{
				sqltype.RegressionSlope: "1", sqltype.RegressionSxx: "2", sqltype.RegressionSxy: "2", sqltype.RegressionSyy: "2",
				sqltype.RegressionCorr: "1", sqltype.RegressionCovarPop: "1", sqltype.RegressionCovarSamp: "2",
				sqltype.RegressionAvgx: "2", sqltype.RegressionAvgy: "2", sqltype.RegressionIntercept: "0", sqltype.RegressionR2: "1",
			}[sqltype.RegressionOperation(final)]
		} else if final == -2 {
			want = "2"
		}
		if err != nil || text != want {
			t.Fatal(text, want, err)
		}
	}
}

func TestStatisticalAggregateEagerCancellation(t *testing.T) {
	for _, call := range []string{"var_pop(1::numeric)", "stddev_pop(1::numeric)", "var_samp(1::double precision)", "regr_sxx(2,1)", "regr_intercept(2,1)", "regr_count(2,1)"} {
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
