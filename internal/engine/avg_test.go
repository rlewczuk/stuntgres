package engine

import (
	"context"
	"stuntgres/internal/engine/exec"
	"stuntgres/internal/engine/functions"
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/engine/plan"
	"stuntgres/internal/sqltype"
	"testing"
)

func avgUnitProvider(e *Engine, id sqltype.ID) (functions.AggregateProvider, *sqltype.Descriptor) {
	input, _ := e.CatalogRead().LookupType(id)
	if id == sqltype.Float4 || id == sqltype.Float8 {
		result, _ := e.CatalogRead().LookupType(sqltype.Float8)
		return functions.NewFloatAvg(input, result.Usage()), input
	}
	result, _ := e.CatalogRead().LookupType(sqltype.Numeric)
	return functions.NewNumericAvg(input, result.Usage()), input
}

func TestAvgViewProviderRemapping(t *testing.T) {
	source, restored := New(), New()
	ctx := context.Background()
	for _, id := range []sqltype.ID{sqltype.Int2, sqltype.Int4, sqltype.Int8, sqltype.Numeric, sqltype.Float4, sqltype.Float8} {
		provider, input := avgUnitProvider(source, id)
		node := &plan.AggregateNode{Child: &plan.SingletonNode{}, Calls: []plan.AggregateCall{{Provider: provider}}}
		mapped, err := remapViewQuery(restored.CatalogRead(), node, func(ref ids.ObjectRef) ids.ObjectRef { return ref }, func(old *sqltype.Descriptor) *sqltype.Descriptor {
			typ, ok := restored.CatalogRead().LookupType(old.ID())
			if !ok {
				t.Fatalf("missing restored type %v", old.ID())
			}
			return typ
		})
		if err != nil {
			t.Fatal(err)
		}
		next := mapped.(*plan.AggregateNode).Calls[0].Provider
		want, restoredInput := avgUnitProvider(restored, id)
		if next.ResultUsage() != want.ResultUsage() || provider.ResultUsage().Type == next.ResultUsage().Type {
			t.Fatal("result descriptor was not independently remapped")
		}
		state, err := next.Init(ctx)
		if err != nil {
			t.Fatal(err)
		}
		foreign, err := input.Input(ctx, "1", -1)
		if err != nil {
			t.Fatal(err)
		}
		if err := next.Advance(ctx, state, foreign, sqltype.Value{}); err == nil {
			t.Fatal("restored provider retained source input ownership")
		}
		for _, text := range []string{"1", "3"} {
			value, err := restoredInput.Input(ctx, text, -1)
			if err != nil {
				t.Fatal(err)
			}
			if err := next.Advance(ctx, state, value, sqltype.Value{}); err != nil {
				t.Fatal(err)
			}
		}
		value, err := next.Finalize(ctx, state)
		if err != nil || value.Type() != want.ResultUsage().Type {
			t.Fatal("restored output ownership", value, err)
		}
		text, err := value.Type().Output(ctx, value)
		expected := "2.0000000000000000"
		if id == sqltype.Float4 || id == sqltype.Float8 {
			expected = "2"
		}
		if err != nil || text != expected {
			t.Fatal(text, expected, err)
		}
		originalState, err := provider.Init(ctx)
		if err != nil {
			t.Fatal(err)
		}
		if err := provider.Advance(ctx, originalState, foreign, sqltype.Value{}); err != nil {
			t.Fatal("remapping mutated original provider", err)
		}
	}
}

func TestAvgSlotsAndMetadata(t *testing.T) {
	_, q, source := queryCoreFixture(t, "SELECT avg(1),avg(1),avg(1::bigint),avg(1::numeric),avg(1::real),avg(1::double precision),avg(INTERVAL '1 day'),count(*) FROM t")
	defer source.Close()
	node := q.Root.(*plan.ProjectNode).Child.(*plan.AggregateNode)
	if len(node.Calls) != 7 {
		t.Fatal("input-aware immutable sharing", len(node.Calls))
	}
	for i, operation := range []functions.AggregateOperation{functions.AggregateNumericAvg, functions.AggregateNumericAvg, functions.AggregateNumericAvg, functions.AggregateFloatAvg, functions.AggregateFloatAvg, functions.AggregateIntervalAvg, functions.AggregateCount} {
		if node.Calls[i].Operation != operation {
			t.Fatal("operation", i, node.Calls[i].Operation)
		}
	}
	for i, c := range q.Root.Schema()[:6] {
		want := sqltype.Numeric
		if i >= 4 {
			want = sqltype.Float8
		}
		if c.Usage.Type.ID() != want || c.Usage.Typmod != -1 || !c.NullabilityKnown || !c.Nullable || c.OriginTable != 0 || c.OriginAttribute != 0 {
			t.Fatal(c)
		}
	}
	_, volatile, owner := queryCoreFixture(t, "SELECT avg(CASE WHEN nextval($1)>0 THEN 1 ELSE 0 END),avg(CASE WHEN nextval($1)>0 THEN 1 ELSE 0 END) FROM t")
	defer owner.Close()
	if len(volatile.Root.(*plan.ProjectNode).Child.(*plan.AggregateNode).Calls) != 2 {
		t.Fatal("volatile calls shared")
	}
}

func TestAvgEagerCancellation(t *testing.T) {
	for _, cast := range []string{"numeric", "double precision"} {
		for _, phase := range []string{"advance", "finalize", "prepublication"} {
			_, q, source := queryCoreFixture(t, "SELECT avg(1::"+cast+") FROM t")
			project := q.Root.(*plan.ProjectNode)
			node := project.Child.(*plan.AggregateNode)
			ctx, cancel := context.WithCancel(context.Background())
			original := node.Calls[0].Provider
			switch phase {
			case "advance":
				node.Calls[0].Provider = aggregateProbeProvider{AggregateProvider: original, advance: func(ctx context.Context, s any, v, separator sqltype.Value) error {
					err := original.Advance(ctx, s, v, separator)
					cancel()
					return err
				}}
			case "finalize":
				node.Calls[0].Provider = aggregateProbeProvider{AggregateProvider: original, finalize: func(ctx context.Context, s any) (sqltype.Value, error) {
					v, err := original.Finalize(ctx, s)
					cancel()
					return v, err
				}}
			case "prepublication":
				expr := project.Expressions[0]
				project.Expressions[0] = queryProbe(expr.ResultUsage(), func(c *exec.Context) (sqltype.Value, error) { v, err := expr.Eval(c); cancel(); return v, err })
			}
			rows, count, err := exec.Finalize(ctx, q.Root, source, nil)
			cancel()
			if rows != nil || count != 0 || err != context.Canceled || source.View != nil || source.Anonymous != nil {
				t.Fatal(cast, phase, rows, count, err)
			}
		}
	}
}
