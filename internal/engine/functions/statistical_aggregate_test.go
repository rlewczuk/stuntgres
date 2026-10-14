package functions

import (
	"context"
	"stuntgres/internal/sqltype"
	"sync"
	"testing"
)

func TestStatisticalAggregateProviders(t *testing.T) {
	ctx := context.Background()
	types := numericUnitTypes()
	for _, id := range []sqltype.ID{sqltype.Int2, sqltype.Int4, sqltype.Int8, sqltype.Numeric, sqltype.Float4, sqltype.Float8} {
		result := types[sqltype.Numeric]
		if id == sqltype.Float4 || id == sqltype.Float8 {
			result = types[sqltype.Float8]
		}
		p := NewStatisticalAggregate(types[id], result.Usage(), false, true)
		state, err := p.Init(ctx)
		if err != nil {
			t.Fatal(err)
		}
		isolated, err := p.Init(ctx)
		if err != nil {
			t.Fatal(err)
		}
		for _, text := range []string{"1", "3"} {
			v, err := types[id].Input(ctx, text, -1)
			if err != nil {
				t.Fatal(err)
			}
			if err := p.Advance(ctx, state, v, sqltype.Value{}); err != nil {
				t.Fatal(err)
			}
		}
		v, err := p.Finalize(ctx, state)
		if err != nil {
			t.Fatal(err)
		}
		text, err := result.Output(ctx, v)
		want := "1.00000000000000000000"
		if result.ID() == sqltype.Float8 {
			want = "1"
		}
		if err != nil || text != want {
			t.Fatal(text, want, err)
		}
		v, err = p.Finalize(ctx, isolated)
		if err != nil || !v.IsNull() {
			t.Fatal("isolated state", err)
		}
		foreign, _ := numericUnitTypes()[id].Input(ctx, "1", -1)
		if err := p.Advance(ctx, state, foreign, sqltype.Value{}); err == nil {
			t.Fatal("foreign descriptor accepted")
		}
		for _, bad := range []any{nil, struct{}{}, (*sqltype.NumericMomentsState)(nil), (*sqltype.FloatAvgState)(nil)} {
			if _, err := p.Finalize(ctx, bad); err == nil {
				t.Fatal("invalid final state accepted")
			}
		}
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
				one, err := types[id].Input(ctx, "1", -1)
				if err != nil {
					t.Error(err)
					return
				}
				if err := p.Advance(ctx, s, one, sqltype.Value{}); err != nil {
					t.Error(err)
					return
				}
				v, err := p.Finalize(ctx, s)
				if err != nil || v.IsNull() {
					t.Error("concurrent state", err)
				}
			}()
		}
		wg.Wait()
	}
	d := types[sqltype.Float8]
	p := NewRegressionAggregate(d, d.Usage(), sqltype.RegressionSxy)
	state, _ := p.Init(ctx)
	one, _ := d.Input(ctx, "1", -1)
	foreign, _ := numericUnitTypes()[sqltype.Float8].Input(ctx, "1", -1)
	if err := p.Advance(ctx, state, one, foreign); err == nil {
		t.Fatal("second argument ownership unchecked")
	}
	if err := p.Advance(ctx, state, one, sqltype.Null(d)); err != nil {
		t.Fatal(err)
	}
	v, err := p.Finalize(ctx, state)
	if err != nil || !v.IsNull() {
		t.Fatal("second NULL was admitted", err)
	}
}

func TestStatisticalAggregateProviderCancellation(t *testing.T) {
	types := numericUnitTypes()
	ctx := context.Background()
	for _, id := range []sqltype.ID{sqltype.Numeric, sqltype.Float8} {
		result := types[id]
		p := NewStatisticalAggregate(types[id], result.Usage(), false, true)
		state, err := p.Init(ctx)
		if err != nil {
			t.Fatal(err)
		}
		for _, text := range []string{"1", "3"} {
			v, _ := types[id].Input(ctx, text, -1)
			if err := p.Advance(ctx, state, v, sqltype.Value{}); err != nil {
				t.Fatal(err)
			}
		}
		// Sweep deterministic fences through arithmetic, including decimal sqrt;
		// each canceled result must remain unpublished, with reusable state.
		for at := 1; at <= 10; at++ {
			probe := &avgCheckContext{Context: ctx, cancelAt: at}
			v, err := p.Finalize(probe, state)
			if err == context.Canceled && v.Type() != nil {
				t.Fatal("cancellation published value")
			}
			if err != nil && err != context.Canceled {
				t.Fatal(err)
			}
			if v, err := p.Finalize(ctx, state); err != nil || v.IsNull() {
				t.Fatal("canceled finalization poisoned state", err)
			}
		}
	}
}
