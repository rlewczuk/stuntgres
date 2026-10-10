package engine

import (
	"context"
	"errors"
	"testing"

	"stuntgres/internal/sqltype"
)

func arrayAggregateUnitProvider(t *testing.T, arrays bool) *arrayAggregateProvider {
	t.Helper()
	e := New()
	element, ok := e.CatalogRead().LookupType(sqltype.Int4)
	if !ok {
		t.Fatal("integer type")
	}
	array, ok := e.CatalogRead().LookupArray(element)
	if !ok {
		t.Fatal("integer array")
	}
	arg := element
	if arrays {
		arg = array
	}
	return &arrayAggregateProvider{argument: arg, usage: array.Usage(), arrays: arrays}
}
func arrayAggregateUnitInput(t *testing.T, p *arrayAggregateProvider, input string) sqltype.Value {
	t.Helper()
	v, err := p.argument.Input(context.Background(), input, -1)
	if err != nil {
		t.Fatal(err)
	}
	return v
}
func arrayAggregateUnitOutput(t *testing.T, p *arrayAggregateProvider, state any, want string) sqltype.Value {
	t.Helper()
	v, err := p.Finalize(context.Background(), state)
	if err != nil {
		t.Fatal(err)
	}
	s, err := p.usage.Type.Output(context.Background(), v)
	if err != nil || s != want {
		t.Fatalf("output=%q want %q: %v", s, want, err)
	}
	return v
}
func TestArrayAggregateScalarState(t *testing.T) {
	ctx := context.Background()
	p := arrayAggregateUnitProvider(t, false)
	empty, err := p.Init(ctx)
	if err != nil {
		t.Fatal(err)
	}
	v, err := p.Finalize(ctx, empty)
	if err != nil || !v.IsNull() || v.Type() != p.usage.Type {
		t.Fatal(v, err)
	}
	state, err := p.Init(ctx)
	if err != nil {
		t.Fatal(err)
	}
	for _, v := range []sqltype.Value{arrayAggregateUnitInput(t, p, "3"), sqltype.Null(p.argument), arrayAggregateUnitInput(t, p, "1")} {
		if err := p.Advance(ctx, state, v, sqltype.Value{}); err != nil {
			t.Fatal(err)
		}
	}
	out := arrayAggregateUnitOutput(t, p, state, "{3,NULL,1}")
	if out.ArrayRank() != 1 || out.ArrayDimension(0) != (sqltype.ArrayDimension{Lower: 1, Length: 3}) {
		t.Fatal("scalar shape")
	}
	again, err := p.Finalize(ctx, state)
	if err != nil || again != out {
		t.Fatal("final result was not cached", err)
	}
	if err := p.Advance(ctx, state, sqltype.Null(p.argument), sqltype.Value{}); err == nil {
		t.Fatal("post-finalize transition accepted")
	}
	other, _ := p.Init(ctx)
	for range 3 {
		if err := p.Advance(ctx, other, sqltype.Null(p.argument), sqltype.Value{}); err != nil {
			t.Fatal(err)
		}
	}
	arrayAggregateUnitOutput(t, p, other, "{NULL,NULL,NULL}")
	if out.ArrayLen() != 3 || out.ArrayElement(0).IsNull() {
		t.Fatal("other group mutated publication")
	}
}
func TestArrayAggregateArrayState(t *testing.T) {
	ctx := context.Background()
	p := arrayAggregateUnitProvider(t, true)
	state, err := p.Init(ctx)
	if err != nil {
		t.Fatal(err)
	}
	first := arrayAggregateUnitInput(t, p, "[-1:0][3:4]={{1,NULL},{2,3}}")
	for _, v := range []sqltype.Value{first, arrayAggregateUnitInput(t, p, "[-1:0][3:4]={{4,5},{NULL,6}}")} {
		if err := p.Advance(ctx, state, v, sqltype.Value{}); err != nil {
			t.Fatal(err)
		}
	}
	arrayAggregateUnitOutput(t, p, state, "[1:2][-1:0][3:4]={{{1,NULL},{2,3}},{{4,5},{NULL,6}}}")
	text, err := p.argument.Output(ctx, first)
	if err != nil || text != "[-1:0][3:4]={{1,NULL},{2,3}}" {
		t.Fatal("source mutation", text, err)
	}
	for _, tc := range []struct{ first, next, code, msg string }{
		{"", "{}", "2202E", "cannot accumulate empty arrays"},
		{"", "NULL", "22004", "cannot accumulate null arrays"},
		{"{1,2}", "NULL", "22004", "cannot accumulate null arrays"},
		{"{1,2}", "{}", "2202E", "cannot accumulate arrays of different dimensionality"},
		{"{1,2}", "{3}", "2202E", "cannot accumulate arrays of different dimensionality"},
		{"{1,2}", "[0:1]={3,4}", "2202E", "cannot accumulate arrays of different dimensionality"},
		{"{1,2}", "{{3,4}}", "2202E", "cannot accumulate arrays of different dimensionality"},
		{"", "{{{{{{7}}}}}}", "54000", "number of array dimensions (7) exceeds the maximum allowed (6)"},
	} {
		t.Run(tc.first+"/"+tc.next, func(t *testing.T) {
			s, _ := p.Init(ctx)
			if tc.first != "" {
				if err := p.Advance(ctx, s, arrayAggregateUnitInput(t, p, tc.first), sqltype.Value{}); err != nil {
					t.Fatal(err)
				}
			}
			v := sqltype.Null(p.argument)
			if tc.next != "NULL" {
				v = arrayAggregateUnitInput(t, p, tc.next)
			}
			err := p.Advance(ctx, s, v, sqltype.Value{})
			var state interface{ SQLState() string }
			if !errors.As(err, &state) || state.SQLState() != tc.code || err.Error() != "stuntgres: "+tc.msg {
				t.Fatalf("runtime error=%v want %s/%s", err, tc.code, tc.msg)
			}
		})
	}
	s, _ := p.Init(ctx)
	if err := p.Advance(ctx, s, arrayAggregateUnitInput(t, p, "{{{{{7}}}}}"), sqltype.Value{}); err != nil {
		t.Fatal(err)
	}
	arrayAggregateUnitOutput(t, p, s, "{{{{{{7}}}}}}")
}
func TestArrayAggregateOwnership(t *testing.T) {
	ctx := context.Background()
	p := arrayAggregateUnitProvider(t, true)
	other := *p
	state, _ := p.Init(ctx)
	for _, bad := range []any{nil, struct{}{}, (*arrayAggregateState)(nil), &arrayAggregateState{}, state} {
		provider := p
		if bad == state {
			provider = &other
		}
		if err := provider.Advance(ctx, bad, sqltype.Null(p.argument), sqltype.Value{}); err == nil {
			t.Fatal("foreign state accepted")
		}
		if _, err := provider.Finalize(ctx, bad); err == nil {
			t.Fatal("foreign final state accepted")
		}
	}
	foreign, err := sqltype.Array(p.argument.ID(), p.argument.Name(), p.argument.Element())
	if err != nil {
		t.Fatal(err)
	}
	if err := p.Advance(ctx, state, sqltype.Null(foreign), sqltype.Value{}); err == nil {
		t.Fatal("foreign descriptor generation accepted")
	}
	if _, err := (&arrayAggregateProvider{}).Init(ctx); err == nil {
		t.Fatal("invalid descriptor accepted")
	}
}

// Err deterministically cancels after the provider enters an actual shape or
// flattening/finalization step; no sleeps, giant buffers, or mock providers.
type arrayAggregateCancelContext struct {
	context.Context
	remaining int
}

func (c *arrayAggregateCancelContext) Err() error {
	c.remaining--
	if c.remaining <= 0 {
		return context.Canceled
	}
	return nil
}
func TestArrayAggregateCancellation(t *testing.T) {
	p := arrayAggregateUnitProvider(t, true)
	ctx := context.Background()
	v := arrayAggregateUnitInput(t, p, "[-1:0][3:4]={{1,NULL},{2,3}}")
	if state, err := p.Init(&arrayAggregateCancelContext{Context: ctx, remaining: 1}); state != nil || err != context.Canceled {
		t.Fatal("initialization cancellation", state, err)
	}
	for _, checks := range []int{1, 2, 4, 6} {
		s, _ := p.Init(ctx)
		probe := &arrayAggregateCancelContext{Context: ctx, remaining: checks}
		if err := p.Advance(probe, s, v, sqltype.Value{}); err != context.Canceled {
			t.Fatal("advance cancellation", checks, err)
		}
	}
	s, _ := p.Init(ctx)
	if err := p.Advance(ctx, s, v, sqltype.Value{}); err != nil {
		t.Fatal(err)
	}
	shapeProbe := &arrayAggregateCancelContext{Context: ctx, remaining: 2}
	if err := p.Advance(shapeProbe, s, v, sqltype.Value{}); err != context.Canceled {
		t.Fatal("later shape comparison cancellation", err)
	}
	probe := &arrayAggregateCancelContext{Context: ctx, remaining: 2}
	out, err := p.Finalize(probe, s)
	if err != context.Canceled || out.Type() != nil {
		t.Fatal("finalization published after cancellation", out, err)
	}
	if s.(*arrayAggregateState).finalized {
		t.Fatal("canceled finalization cached publication")
	}
	arrayAggregateUnitOutput(t, p, s, "[1:1][-1:0][3:4]={{{1,NULL},{2,3}}}")
}
func TestArrayAggregateDescription(t *testing.T) {
	_, query, source := queryCoreFixture(t, "SELECT array_agg(id ORDER BY id),array_agg(NULL::integer[]) FROM t")
	defer source.close()
	for _, column := range query.Root.Schema() {
		if column.Name != "array_agg" || column.Usage.Type.ID() != 1007 || column.Usage.Typmod != -1 || column.OriginTable != 0 || column.OriginAttribute != 0 || !column.Nullable || !column.NullabilityKnown {
			t.Fatal("aggregate description", column)
		}
	}
}
