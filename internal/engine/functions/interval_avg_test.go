package functions

import (
	"context"
	"errors"
	"stuntgres/internal/sqltype"
	"testing"
)

func TestIntervalAvgProvider(t *testing.T) {
	e := numericUnitTypes()
	typ, ok := e[sqltype.Interval]
	if !ok {
		t.Fatal("missing interval descriptor")
	}
	p := NewIntervalAvg(typ.Usage())
	ctx := context.Background()
	state, err := p.Init(ctx)
	if err != nil {
		t.Fatal(err)
	}
	isolated, err := p.Init(ctx)
	if err != nil {
		t.Fatal(err)
	}
	for _, days := range []int32{1, 3} {
		v, _ := sqltype.IntervalValue(typ, 0, days, 0)
		if err := p.Advance(ctx, state, v, sqltype.Value{}); err != nil {
			t.Fatal(err)
		}
	}
	for range 2 {
		v, err := p.Finalize(ctx, state)
		if err != nil {
			t.Fatal(err)
		}
		m, d, u, err := v.Interval()
		if err != nil || m != 0 || d != 2 || u != 0 || v.Type() != typ {
			t.Fatal(m, d, u, err)
		}
	}
	v, err := p.Finalize(ctx, isolated)
	if err != nil || !v.IsNull() || v.Type() != typ {
		t.Fatal("group state leaked", v, err)
	}
	if p.ResultUsage() != typ.Usage() || p.ResultUsage().Typmod != -1 {
		t.Fatal("result usage", p.ResultUsage())
	}
	foreignType := numericUnitTypes()[sqltype.Interval]
	wrong, _ := sqltype.IntervalValue(foreignType, 0, 1, 0)
	for _, invalid := range []sqltype.Value{{}, wrong} {
		err := p.Advance(ctx, state, invalid, sqltype.Value{})
		var s interface{ SQLState() string }
		if err == nil || errors.As(err, &s) {
			t.Fatal("invalid ownership must be ordinary error", err)
		}
	}
	for _, invalid := range []any{nil, struct{}{}, (*sqltype.IntervalAvgState)(nil)} {
		if err := p.Advance(ctx, invalid, v, sqltype.Value{}); err == nil {
			t.Fatal("invalid state accepted")
		}
		if _, err := p.Finalize(ctx, invalid); err == nil {
			t.Fatal("invalid final state accepted")
		}
	}
}

func TestIntervalAvgProviderCancellation(t *testing.T) {
	e := numericUnitTypes()
	typ := e[sqltype.Interval]
	p := NewIntervalAvg(typ.Usage())
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	state, err := p.Init(ctx)
	if state != nil || err != context.Canceled {
		t.Fatal(state, err)
	}
	state, err = p.Init(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	v, _ := sqltype.IntervalValue(typ, 0, 1, 0)
	if err := p.Advance(ctx, state, v, sqltype.Value{}); err != context.Canceled {
		t.Fatal(err)
	}
	out, err := p.Finalize(ctx, state)
	if err != context.Canceled || out.Type() != nil {
		t.Fatal(out, err)
	}
	out, err = p.Finalize(context.Background(), state)
	if err != nil || !out.IsNull() {
		t.Fatal("canceled Advance changed state", out, err)
	}
	// Invalid states cannot replace the original cancellation error.
	if err := p.Advance(ctx, nil, v, sqltype.Value{}); err != context.Canceled {
		t.Fatal(err)
	}
	if _, err := p.Finalize(ctx, nil); err != context.Canceled {
		t.Fatal(err)
	}
}
