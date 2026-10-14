package functions

import (
	"context"
	"errors"
	"stuntgres/internal/sqltype"
	"sync"
	"testing"
)

func avgUnitProvider(types map[sqltype.ID]*sqltype.Descriptor, id sqltype.ID) (numericProviderUnit, *sqltype.Descriptor) {
	input := types[id]
	if id == sqltype.Float4 || id == sqltype.Float8 {
		return NewFloatAvg(input, types[sqltype.Float8].Usage()), input
	}
	return NewNumericAvg(input, types[sqltype.Numeric].Usage()), input
}

func TestAvgProviders(t *testing.T) {
	for _, id := range []sqltype.ID{sqltype.Int2, sqltype.Int4, sqltype.Int8, sqltype.Numeric, sqltype.Float4, sqltype.Float8} {
		e := numericUnitTypes()
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
		foreign := numericUnitTypes()[id]
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
		p, input := avgUnitProvider(numericUnitTypes(), id)
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
