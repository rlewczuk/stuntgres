package engine

import (
	"context"
	"errors"
	"stuntgres/internal/sqltype"
	"testing"
)

func TestIntervalAvgProvider(t *testing.T) {
	e := New()
	typ, ok := e.CatalogRead().LookupType(sqltype.Interval)
	if !ok {
		t.Fatal("missing interval descriptor")
	}
	p := intervalAvgProvider{typ.Usage()}
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
	foreign := New()
	foreignType, _ := foreign.CatalogRead().LookupType(sqltype.Interval)
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
	e := New()
	typ, _ := e.CatalogRead().LookupType(sqltype.Interval)
	p := intervalAvgProvider{typ.Usage()}
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

func TestIntervalAvgSlotsAndMetadata(t *testing.T) {
	_, q, source := queryCoreFixture(t, "SELECT avg(INTERVAL '1 day'),avg(INTERVAL '1 day'),count(*),bool_and(f) FROM t")
	defer source.close()
	node := q.Root.(*projectNode).child.(*aggregateNode)
	if len(node.calls) != 3 {
		t.Fatal("operation sharing", len(node.calls))
	}
	if node.calls[0].operation != aggregateIntervalAvg {
		t.Fatal("wrong operation")
	}
	for _, c := range q.Root.Schema()[:2] {
		if !c.NullabilityKnown || !c.Nullable || c.OriginTable != 0 || c.OriginAttribute != 0 || c.Usage.Typmod != -1 || c.Usage.Type.ID() != sqltype.Interval {
			t.Fatal(c)
		}
	}
	// The actual provider remains execution-owned even when immutable calls share.
	rows, n, err := finalizeQuery(context.Background(), q.Root, source, nil)
	if err != nil || n != 1 {
		t.Fatal(n, err)
	}
	defer rows.Close()
	values := make([]sqltype.Value, 4)
	present, err := rows.Next(context.Background(), values)
	if !present || err != nil {
		t.Fatal(present, err)
	}
	for _, v := range values[:2] {
		m, d, u, err := v.Interval()
		if err != nil || m != 0 || d != 1 || u != 0 {
			t.Fatal(m, d, u, err)
		}
	}
	_, volatile, owner := queryCoreFixture(t, "SELECT avg(CASE WHEN nextval($1)>0 THEN INTERVAL '1 day' ELSE INTERVAL '0' END),avg(CASE WHEN nextval($1)>0 THEN INTERVAL '1 day' ELSE INTERVAL '0' END) FROM t")
	defer owner.close()
	if len(volatile.Root.(*projectNode).child.(*aggregateNode).calls) != 2 {
		t.Fatal("volatile average arguments shared")
	}
}
