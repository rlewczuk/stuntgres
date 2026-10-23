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

func sumUnitProvider(types map[sqltype.ID]*sqltype.Descriptor, id sqltype.ID) (numericProviderUnit, *sqltype.Descriptor) {
	input := types[id]
	switch id {
	case sqltype.Int2, sqltype.Int4:
		return NewIntegralSum(input, types[sqltype.Int8].Usage()), input
	case sqltype.Int8, sqltype.Numeric:
		return NewNumericSum(input, types[sqltype.Numeric].Usage()), input
	default:
		return NewFloatSum(input, input.Usage()), input
	}
}

func TestSumProviders(t *testing.T) {
	ctx := context.Background()
	for _, id := range []sqltype.ID{sqltype.Int2, sqltype.Int4, sqltype.Int8, sqltype.Numeric, sqltype.Float4, sqltype.Float8} {
		p, input := sumUnitProvider(numericUnitTypes(), id)
		state, err := p.Init(ctx)
		if err != nil {
			t.Fatal(err)
		}
		empty, err := p.Finalize(ctx, state)
		if err != nil || !empty.IsNull() || empty.Type() != p.ResultUsage().Type || p.ResultUsage().Typmod != -1 {
			t.Fatal(empty, err)
		}
		for _, text := range []string{"1", "3"} {
			v, err := input.Input(ctx, text, -1)
			if err != nil {
				t.Fatal(err)
			}
			if err := p.Advance(ctx, state, []sqltype.Value{v, sqltype.Value{}}, nil); err != nil {
				t.Fatal(err)
			}
		}
		if err := p.Advance(ctx, state, []sqltype.Value{sqltype.Null(input), sqltype.Value{}}, nil); err != nil {
			t.Fatal(err)
		}
		for range 2 {
			v, err := p.Finalize(ctx, state)
			if err != nil || v.Type() != p.ResultUsage().Type {
				t.Fatal(v, err)
			}
			text, err := v.Type().Output(ctx, v)
			if err != nil || text != "4" {
				t.Fatal(text, err)
			}
		}
		foreign := numericUnitTypes()[id]
		wrong, _ := foreign.Input(ctx, "1", -1)
		for _, invalid := range []sqltype.Value{{}, wrong} {
			err := p.Advance(ctx, state, []sqltype.Value{invalid, sqltype.Value{}}, nil)
			var diagnostic *sqlstate.Error
			if err == nil || errors.As(err, &diagnostic) {
				t.Fatal("ownership error must remain ordinary", err)
			}
		}
		for _, invalid := range []any{nil, struct{}{}, (*integralSumState)(nil), (*sqltype.NumericSumState)(nil), (*floatSumState)(nil)} {
			if p.Advance(ctx, invalid, []sqltype.Value{sqltype.Null(input), sqltype.Value{}}, nil) == nil {
				t.Fatal("invalid state accepted")
			}
			if _, err := p.Finalize(ctx, invalid); err == nil {
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
				one, err := input.Input(ctx, "1", -1)
				if err != nil {
					t.Error(err)
					return
				}
				if err := p.Advance(ctx, s, []sqltype.Value{one, sqltype.Value{}}, nil); err != nil {
					t.Error(err)
					return
				}
				out, err := p.Finalize(ctx, s)
				if err != nil {
					t.Error(err)
					return
				}
				text, err := out.Type().Output(ctx, out)
				if err != nil || text != "1" {
					t.Error("execution state not isolated", text, err)
				}
			}()
		}
		wg.Wait()
	}
}

func TestSumIntegralTransitions(t *testing.T) {
	ctx := context.Background()
	for _, id := range []sqltype.ID{sqltype.Int2, sqltype.Int4} {
		p, input := sumUnitProvider(numericUnitTypes(), id)
		state, err := p.Init(ctx)
		if err != nil {
			t.Fatal(err)
		}
		one, _ := sqltype.Integer(input, 1)
		// State boxing, datum construction and argument scratch precede measurement.
		arguments := []sqltype.Value{one}
		if err := p.Advance(ctx, state, arguments, nil); err != nil {
			t.Fatal(err)
		}
		if n := testing.AllocsPerRun(100, func() {
			if err := p.Advance(ctx, state, arguments, nil); err != nil {
				panic(err)
			}
		}); n != 0 {
			t.Fatal("integral transition allocations", n)
		}
		// Source-derived unchecked transition boundary, not a fabricated SQL overflow.
		s := state.(*integralSumState)
		s.sum = math.MaxInt64
		if err := p.Advance(ctx, state, arguments, nil); err != nil || s.sum != math.MinInt64 {
			t.Fatal("released signed64 transition", s.sum, err)
		}
	}
}

func TestSumFloatingTransitions(t *testing.T) {
	ctx := context.Background()
	for _, tc := range []struct {
		id     sqltype.ID
		values []float64
		want   float64
	}{
		{sqltype.Float4, []float64{16777216, 1, -16777216}, 0},
		{sqltype.Float8, []float64{9007199254740992, 1, -9007199254740992}, 0},
		{sqltype.Float4, []float64{math.SmallestNonzeroFloat32, math.SmallestNonzeroFloat32}, 2 * math.SmallestNonzeroFloat32},
		{sqltype.Float4, []float64{math.Copysign(0, -1), math.Copysign(0, -1)}, math.Copysign(0, -1)},
		{sqltype.Float8, []float64{math.Copysign(0, -1)}, math.Copysign(0, -1)},
		{sqltype.Float4, []float64{math.Inf(1), 1}, math.Inf(1)},
		{sqltype.Float8, []float64{math.Inf(-1), math.Inf(1)}, math.NaN()},
	} {
		p, input := sumUnitProvider(numericUnitTypes(), tc.id)
		state, err := p.Init(ctx)
		if err != nil {
			t.Fatal(err)
		}
		for _, n := range tc.values {
			v, err := sqltype.Float(input, n)
			if err != nil {
				t.Fatal(err)
			}
			if err := p.Advance(ctx, state, []sqltype.Value{sqltype.Null(input), sqltype.Value{}}, nil); err != nil {
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
		got, err := v.Float()
		if err != nil || math.IsNaN(tc.want) != math.IsNaN(got) || !math.IsNaN(got) && (got != tc.want || math.Signbit(got) != math.Signbit(tc.want)) {
			t.Fatal(tc, got, err)
		}
	}
	for _, id := range []sqltype.ID{sqltype.Float4, sqltype.Float8} {
		p, input := sumUnitProvider(numericUnitTypes(), id)
		state, err := p.Init(ctx)
		if err != nil {
			t.Fatal(err)
		}
		maximum := math.MaxFloat64
		if id == sqltype.Float4 {
			maximum = math.MaxFloat32
		}
		value, _ := sqltype.Float(input, maximum)
		if err := p.Advance(ctx, state, []sqltype.Value{value, sqltype.Value{}}, nil); err != nil {
			t.Fatal(err)
		}
		err = p.Advance(ctx, state, []sqltype.Value{value, sqltype.Value{}}, nil)
		var d *sqlstate.Error
		if !errors.As(err, &d) || d.Code != "22003" || d.Message != "value out of range: overflow" || d.Position != "" {
			t.Fatal("finite transition overflow", err)
		}
	}
}

// Cancellation is triggered on an actual provider context check and propagates
// through a real context's Err/Done; no sleeps or production hooks are needed.
type sumCancelContext struct {
	context.Context
	cancel     context.CancelFunc
	checks, at int
}

func (c *sumCancelContext) Err() error {
	c.checks++
	if c.checks == c.at {
		c.cancel()
	}
	return c.Context.Err()
}
func sumCancellationAt(at int) (*sumCancelContext, context.CancelFunc) {
	ctx, cancel := context.WithCancel(context.Background())
	return &sumCancelContext{Context: ctx, cancel: cancel, at: at}, cancel
}
func TestSumProviderCancellation(t *testing.T) {
	for _, id := range []sqltype.ID{sqltype.Int4, sqltype.Numeric, sqltype.Float4} {
		p, input := sumUnitProvider(numericUnitTypes(), id)
		one, _ := input.Input(context.Background(), "1", -1)
		for _, at := range []int{1, 2} {
			ctx, cancel := sumCancellationAt(at)
			state, err := p.Init(ctx)
			cancel()
			if state != nil || err != context.Canceled {
				t.Fatal("Init cancellation", state, err)
			}
			state, err = p.Init(context.Background())
			if err != nil {
				t.Fatal(err)
			}
			ctx, cancel = sumCancellationAt(at)
			err = p.Advance(ctx, state, []sqltype.Value{one, sqltype.Value{}}, nil)
			cancel()
			if err != context.Canceled {
				t.Fatal("Advance cancellation", err)
			}
			out, err := p.Finalize(context.Background(), state)
			if err != nil || out.IsNull() != (at == 1) {
				t.Fatal("transition boundary", out, err)
			}
			ctx, cancel = sumCancellationAt(at)
			out, err = p.Finalize(ctx, state)
			cancel()
			if out.Type() != nil || err != context.Canceled {
				t.Fatal("Finalize cancellation", out, err)
			}
		}
		fresh, err := p.Init(context.Background())
		if err != nil {
			t.Fatal(err)
		}
		out, err := p.Finalize(context.Background(), fresh)
		if err != nil || !out.IsNull() {
			t.Fatal("new execution poisoned", out, err)
		}
	}
}
