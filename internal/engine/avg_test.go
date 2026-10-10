package engine

import (
	"context"
	"errors"
	"stuntgres/internal/sqltype"
	"sync"
	"testing"
)

func avgUnitProvider(e *Engine, id sqltype.ID) (aggregateProvider, *sqltype.Descriptor) {
	input, _ := e.CatalogRead().LookupType(id)
	if id == sqltype.Float4 || id == sqltype.Float8 {
		result, _ := e.CatalogRead().LookupType(sqltype.Float8)
		return floatAvgProvider{input, result.Usage()}, input
	}
	result, _ := e.CatalogRead().LookupType(sqltype.Numeric)
	return numericAvgProvider{input, result.Usage()}, input
}

func TestAvgProviders(t *testing.T) {
	for _, id := range []sqltype.ID{sqltype.Int2, sqltype.Int4, sqltype.Int8, sqltype.Numeric, sqltype.Float4, sqltype.Float8} {
		e := New()
		p, input := avgUnitProvider(e, id)
		ctx := context.Background()
		state, err := p.Init(ctx)
		if err != nil {
			t.Fatal(err)
		}
		isolated, err := p.Init(ctx)
		if err != nil {
			t.Fatal(err)
		}
		for _, text := range []string{"1", "3"} {
			v, err := input.Input(ctx, text, -1)
			if err != nil {
				t.Fatal(err)
			}
			if err := p.Advance(ctx, state, v, sqltype.Value{}); err != nil {
				t.Fatal(err)
			}
		}
		if err := p.Advance(ctx, state, sqltype.Null(input), sqltype.Value{}); err != nil {
			t.Fatal(err)
		}
		for range 2 {
			v, err := p.Finalize(ctx, state)
			if err != nil || v.Type() != p.ResultUsage().Type {
				t.Fatal(v, err)
			}
			text, err := v.Type().Output(ctx, v)
			want := "2.0000000000000000"
			if id == sqltype.Float4 || id == sqltype.Float8 {
				want = "2"
			}
			if err != nil || text != want {
				t.Fatal(text, want, err)
			}
		}
		v, err := p.Finalize(ctx, isolated)
		if err != nil || !v.IsNull() || v.Type() != p.ResultUsage().Type || p.ResultUsage().Typmod != -1 {
			t.Fatal(v, err)
		}
		foreign, _ := New().CatalogRead().LookupType(id)
		wrong, _ := foreign.Input(ctx, "1", -1)
		for _, invalid := range []sqltype.Value{{}, wrong} {
			err := p.Advance(ctx, state, invalid, sqltype.Value{})
			var diagnostic interface{ SQLState() string }
			if err == nil || errors.As(err, &diagnostic) {
				t.Fatal("ownership must be ordinary error", err)
			}
		}
		for _, invalid := range []any{nil, struct{}{}, (*sqltype.NumericAvgState)(nil), (*sqltype.FloatAvgState)(nil)} {
			if err := p.Advance(ctx, invalid, v, sqltype.Value{}); err == nil {
				t.Fatal("invalid state accepted")
			}
			if _, err := p.Finalize(ctx, invalid); err == nil {
				t.Fatal("invalid result state accepted")
			}
		}
		// The same immutable provider may create states in concurrent executions.
		var wg sync.WaitGroup
		for range 4 {
			wg.Add(1)
			go func() {
				defer wg.Done()
				s, err := p.Init(ctx)
				if err != nil {
					t.Error(err)
					return
				}
				one, err := input.Input(ctx, "1", -1)
				if err != nil {
					t.Error(err)
					return
				}
				if err := p.Advance(ctx, s, one, sqltype.Value{}); err != nil {
					t.Error(err)
					return
				}
				out, err := p.Finalize(ctx, s)
				if err != nil || out.IsNull() {
					t.Error("execution state", err)
				}
			}()
		}
		wg.Wait()
	}
}

func TestAvgViewProviderRemapping(t *testing.T) {
	source, restored := New(), New()
	ctx := context.Background()
	for _, id := range []sqltype.ID{sqltype.Int2, sqltype.Int4, sqltype.Int8, sqltype.Numeric, sqltype.Float4, sqltype.Float8} {
		provider, input := avgUnitProvider(source, id)
		node := &aggregateNode{child: &singletonNode{}, calls: []aggregateCall{{provider: provider}}}
		mapped, err := remapViewQuery(restored.CatalogRead(), node, func(ref ObjectRef) ObjectRef { return ref }, func(old *sqltype.Descriptor) *sqltype.Descriptor {
			typ, ok := restored.CatalogRead().LookupType(old.ID())
			if !ok {
				t.Fatalf("missing restored type %v", old.ID())
			}
			return typ
		})
		if err != nil {
			t.Fatal(err)
		}
		next := mapped.(*aggregateNode).calls[0].provider
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

// A deterministic test-owned context cancels at the provider's post-work check.
type avgCheckContext struct {
	context.Context
	checks, cancelAt int
}

func (c *avgCheckContext) Err() error {
	c.checks++
	if c.checks >= c.cancelAt {
		return context.Canceled
	}
	return nil
}

func TestAvgProviderCancellation(t *testing.T) {
	for _, id := range []sqltype.ID{sqltype.Int4, sqltype.Float4} {
		p, input := avgUnitProvider(New(), id)
		ctx, cancel := context.WithCancel(context.Background())
		cancel()
		if state, err := p.Init(ctx); state != nil || err != context.Canceled {
			t.Fatal(state, err)
		}
		state, err := p.Init(context.Background())
		if err != nil {
			t.Fatal(err)
		}
		one, _ := input.Input(context.Background(), "1", -1)
		if err := p.Advance(ctx, state, one, sqltype.Value{}); err != context.Canceled {
			t.Fatal(err)
		}
		if out, err := p.Finalize(ctx, state); out.Type() != nil || err != context.Canceled {
			t.Fatal(out, err)
		}
		out, err := p.Finalize(context.Background(), state)
		if err != nil || !out.IsNull() {
			t.Fatal("canceled transition mutated state", err)
		}
		if state, err := p.Init(&avgCheckContext{Context: context.Background(), cancelAt: 2}); state != nil || err != context.Canceled {
			t.Fatal(state, err)
		}
		if err := p.Advance(&avgCheckContext{Context: context.Background(), cancelAt: 2}, state, one, sqltype.Value{}); err != context.Canceled {
			t.Fatal("post-transition cancellation", err)
		}
		if out, err := p.Finalize(&avgCheckContext{Context: context.Background(), cancelAt: 2}, state); out.Type() != nil || err != context.Canceled {
			t.Fatal("post-result cancellation", out, err)
		}
		fresh, err := p.Init(context.Background())
		if err != nil {
			t.Fatal(err)
		}
		if out, err := p.Finalize(context.Background(), fresh); err != nil || !out.IsNull() {
			t.Fatal("new execution poisoned", err)
		}
	}
}

func TestAvgSlotsAndMetadata(t *testing.T) {
	_, q, source := queryCoreFixture(t, "SELECT avg(1),avg(1),avg(1::bigint),avg(1::numeric),avg(1::real),avg(1::double precision),avg(INTERVAL '1 day'),count(*) FROM t")
	defer source.close()
	node := q.Root.(*projectNode).child.(*aggregateNode)
	if len(node.calls) != 7 {
		t.Fatal("input-aware immutable sharing", len(node.calls))
	}
	for i, operation := range []aggregateOperation{aggregateNumericAvg, aggregateNumericAvg, aggregateNumericAvg, aggregateFloatAvg, aggregateFloatAvg, aggregateIntervalAvg, aggregateCount} {
		if node.calls[i].operation != operation {
			t.Fatal("operation", i, node.calls[i].operation)
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
	defer owner.close()
	if len(volatile.Root.(*projectNode).child.(*aggregateNode).calls) != 2 {
		t.Fatal("volatile calls shared")
	}
}

func TestAvgEagerCancellation(t *testing.T) {
	for _, cast := range []string{"numeric", "double precision"} {
		for _, phase := range []string{"advance", "finalize", "prepublication"} {
			_, q, source := queryCoreFixture(t, "SELECT avg(1::"+cast+") FROM t")
			project := q.Root.(*projectNode)
			node := project.child.(*aggregateNode)
			ctx, cancel := context.WithCancel(context.Background())
			original := node.calls[0].provider
			switch phase {
			case "advance":
				node.calls[0].provider = aggregateProbeProvider{aggregateProvider: original, advance: func(ctx context.Context, s any, v, separator sqltype.Value) error {
					err := original.Advance(ctx, s, v, separator)
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
				t.Fatal(cast, phase, rows, count, err)
			}
		}
	}
}
