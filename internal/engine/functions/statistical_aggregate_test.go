package functions

import (
	"context"
	"errors"
	"math"
	"stuntgres/internal/sqlstate"
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
			if err := p.Advance(ctx, state, []sqltype.Value{v, sqltype.Value{}}, nil); err != nil {
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
		if err := p.Advance(ctx, state, []sqltype.Value{foreign, sqltype.Value{}}, nil); err == nil {
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
				if err := p.Advance(ctx, s, []sqltype.Value{one, sqltype.Value{}}, nil); err != nil {
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
	if err := p.Advance(ctx, state, []sqltype.Value{one, foreign}, nil); err == nil {
		t.Fatal("second argument ownership unchecked")
	}
	if err := p.Advance(ctx, state, []sqltype.Value{one, sqltype.Null(d)}, nil); err != nil {
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
			if err := p.Advance(ctx, state, []sqltype.Value{v, sqltype.Value{}}, nil); err != nil {
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

func TestStatisticalAggregateRegressionCount(t *testing.T) {
	ctx := context.Background()
	types := numericUnitTypes()
	d, result := types[sqltype.Float8], types[sqltype.Int8]
	p := NewRegressionCountAggregate(d, result.Usage())
	state, err := p.Init(ctx)
	if err != nil {
		t.Fatal(err)
	}
	isolated, err := p.Init(ctx)
	if err != nil {
		t.Fatal(err)
	}
	assertCount := func(state any, want int64) {
		t.Helper()
		for range 2 {
			v, err := p.Finalize(ctx, state)
			n, readErr := v.Integer()
			if err != nil || readErr != nil || v.IsNull() || v.Type() != result || n != want {
				t.Fatal("pair count", v, want, err, readErr)
			}
		}
	}
	assertCount(state, 0)
	one, _ := sqltype.Float(d, 1)
	for _, pair := range [][2]sqltype.Value{{sqltype.Null(d), one}, {one, sqltype.Null(d)}, {sqltype.Null(d), sqltype.Null(d)}} {
		if err := p.Advance(ctx, state, []sqltype.Value{pair[0], pair[1]}, nil); err != nil {
			t.Fatal(err)
		}
	}
	assertCount(state, 0)
	foreign, _ := sqltype.Float(numericUnitTypes()[sqltype.Float8], 1)
	for _, pair := range [][2]sqltype.Value{{foreign, one}, {one, foreign}, {sqltype.Null(d), foreign}, {foreign, sqltype.Null(d)}, {{}, one}, {one, {}}} {
		if err := p.Advance(ctx, state, []sqltype.Value{pair[0], pair[1]}, nil); err == nil {
			t.Fatal("invalid descriptor accepted")
		}
	}
	for _, bad := range []any{nil, struct{}{}, (*countState)(nil)} {
		if err := p.Advance(ctx, bad, []sqltype.Value{one, one}, nil); err == nil {
			t.Fatal("invalid advance state accepted")
		}
		if v, err := p.Finalize(ctx, bad); err == nil || v.Type() != nil {
			t.Fatal("invalid final state published", err)
		}
	}
	for _, number := range []float64{1e308, math.Inf(1), math.NaN()} {
		v, _ := sqltype.Float(d, number)
		if err := p.Advance(ctx, state, []sqltype.Value{v, v}, nil); err != nil {
			t.Fatal(err)
		}
	}
	assertCount(state, 3)
	assertCount(isolated, 0)
	s := state.(*countState)
	s.count = 1 << 53
	if err := p.Advance(ctx, state, []sqltype.Value{one, one}, nil); err != nil {
		t.Fatal(err)
	}
	assertCount(state, 9007199254740993)
	s.count = math.MaxInt64 - 1
	if err := p.Advance(ctx, state, []sqltype.Value{one, one}, nil); err != nil {
		t.Fatal(err)
	}
	assertCount(state, math.MaxInt64)
	var diagnostic *sqlstate.Error
	if err := p.Advance(ctx, state, []sqltype.Value{one, one}, nil); !errors.As(err, &diagnostic) || *diagnostic != (sqlstate.Error{Severity: "ERROR", SeverityUnlocalized: "ERROR", Code: "22003", Message: "bigint out of range"}) {
		t.Fatal(err)
	}
	assertCount(state, math.MaxInt64)
	canceled, cancel := context.WithCancel(ctx)
	cancel()
	if value, err := p.Init(canceled); err != context.Canceled || value != nil {
		t.Fatal("canceled init", value, err)
	}
	if err := p.Advance(canceled, state, []sqltype.Value{one, one}, nil); err != context.Canceled {
		t.Fatal("canceled advance", err)
	}
	if value, err := p.Finalize(canceled, state); err != context.Canceled || value.Type() != nil {
		t.Fatal("canceled finalization", value, err)
	}
	assertCount(state, math.MaxInt64)
	// Cancellation after reading the result must still publish no value.
	probe := &avgCheckContext{Context: ctx, cancelAt: 3}
	if value, err := p.Finalize(probe, state); err != context.Canceled || value.Type() != nil {
		t.Fatal("post-work cancellation", value, err)
	}
	s.count = 0
	if allocations := testing.AllocsPerRun(100, func() {
		if err := p.Advance(ctx, state, []sqltype.Value{one, one}, nil); err != nil {
			panic(err)
		}
	}); allocations != 0 {
		t.Fatal("pair count advance allocations", allocations)
	}
}
