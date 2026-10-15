package sqltype

import (
	"context"
	"errors"
	"testing"
)

var moreUnitSink Value

func TestRangeMoreConstruction(t *testing.T) {
	types, fresh := inspectUnitTypes(), inspectUnitTypes()
	ctx := context.Background()
	for _, family := range []struct { r, m ID; input, output string }{
		{Int4Range, Int4Multirange, "[1,4]", "{[1,5)}"},
		{Int8Range, Int8Multirange, "(1,4]", "{[2,5)}"},
		{NumRange, NumMultirange, "(1.00,2.000]", "{(1.00,2.000]}"},
	} {
		r, m := types[family.r], types[family.m]
		if RangeInspectResult(RangeConstruct, r) != m || MultirangeUnnestResult(m) != r { t.Fatal("companion identity") }
		plan, err := BindRangeInspect(RangeConstruct, m.Usage(), r.Usage())
		if err != nil { t.Fatal(err) }
		finite := inspectUnitInput(t, r, family.input)
		got, err := plan.Evaluate(ctx, [2]Value{finite})
		if err != nil { t.Fatal(err) }
		inspectUnitOutput(t, got, family.output)
		if got.typ != m || got.null || len(got.multirange.members) != 1 || got.multirange.members[0] != finite.rangeDatum { t.Fatal("constructor changed canonical datum") }
		for _, input := range []string{"empty", "(,)"} {
			value := inspectUnitInput(t, r, input)
			result, err := plan.Evaluate(ctx, [2]Value{value})
			if err != nil || result.null { t.Fatal(result, err) }
			want := "{}"
			if input == "(,)" { want = "{(,)}" }
			inspectUnitOutput(t, result, want)
		}
		null, err := plan.Evaluate(ctx, [2]Value{Null(r)})
		if err != nil || null.typ != m || !null.null || null.multirange != nil { t.Fatal("strict NULL", err) }
		if _, err := BindRangeInspect(RangeConstruct, fresh[family.m].Usage(), r.Usage()); err == nil { t.Fatal("foreign result") }
		if _, err := plan.Evaluate(ctx, [2]Value{Null(fresh[family.r])}); err == nil { t.Fatal("foreign input") }
		if _, err := BindRangeInspect(RangeConstruct, m.Usage(), r.Usage(), r.Usage()); err == nil { t.Fatal("binary constructor") }
		if allocations := testing.AllocsPerRun(100, func() { moreUnitSink, err = plan.Evaluate(ctx, [2]Value{finite}); if err != nil { panic(err) } }); allocations != 1 { t.Fatal("constructor publication allocations", allocations) }
		empty := inspectUnitInput(t, r, "empty")
		if allocations := testing.AllocsPerRun(100, func() { moreUnitSink, err = plan.Evaluate(ctx, [2]Value{empty}); if err != nil { panic(err) } }); allocations != 0 { t.Fatal("empty allocation", allocations) }
	}
	for _, id := range []ID{DateRange, TimestampRange, TimestamptzRange, Int4Multirange, Int4, Text} {
		if RangeInspectResult(RangeConstruct, types[id]) != nil { t.Fatal("excluded constructor family", id) }
	}
}

func TestRangeMoreCursor(t *testing.T) {
	types, fresh := inspectUnitTypes(), inspectUnitTypes()
	ctx := context.Background()
	for _, id := range []ID{Int4Multirange, Int8Multirange, NumMultirange} {
		m := types[id]
		r := MultirangeUnnestResult(m)
		value := inspectUnitInput(t, m, "{[8,9),empty,[1,3),[2,4)}")
		first, err := OpenMultirange(ctx, m.Usage(), r.Usage(), value)
		if err != nil { t.Fatal(err) }
		second, err := OpenMultirange(ctx, m.Usage(), r.Usage(), value)
		if err != nil { t.Fatal(err) }
		if first.payload != value.multirange || second.payload != first.payload { t.Fatal("cursor copied immutable payload") }
		for i, want := range []string{"[1,4)", "[8,9)"} {
			x, ok, err := first.Next(ctx)
			if err != nil || !ok || x.typ != r || x.rangeDatum != value.multirange.members[i] { t.Fatal(x, ok, err) }
			inspectUnitOutput(t, x, want)
		}
		if x, ok, err := second.Next(ctx); err != nil || !ok || x.rangeDatum != value.multirange.members[0] { t.Fatal("shared progress", x, ok, err) }
		for range 2 { if x, ok, err := first.Next(ctx); err != nil || ok || !x.null || x.typ != r || first.payload != nil { t.Fatal("exhaustion", x, ok, err) } }
		second.Close(); second.Close()
		if _, ok, err := second.Next(ctx); err != nil || ok { t.Fatal("close", ok, err) }
		for _, input := range []Value{Null(m), inspectUnitInput(t, m, "{}"), inspectUnitInput(t, m, "{empty}")} {
			c, err := OpenMultirange(ctx, m.Usage(), r.Usage(), input)
			if err != nil { t.Fatal(err) }
			if x, ok, err := c.Next(ctx); err != nil || ok || !x.null || x.typ != r { t.Fatal(x, ok, err) }
		}
		if _, err := OpenMultirange(ctx, m.Usage(), fresh[r.id].Usage(), value); err == nil { t.Fatal("foreign result") }
		if _, err := OpenMultirange(ctx, m.Usage(), r.Usage(), Null(fresh[id])); err == nil { t.Fatal("foreign value") }
		canceled, cancel := context.WithCancel(ctx); cancel()
		if _, err := OpenMultirange(canceled, m.Usage(), r.Usage(), value); !errors.Is(err, context.Canceled) { t.Fatal(err) }
		c, err := OpenMultirange(ctx, m.Usage(), r.Usage(), value)
		if err != nil { t.Fatal(err) }
		if _, _, err := c.Next(canceled); !errors.Is(err, context.Canceled) || c.payload != nil { t.Fatal("cancellation retention", err) }
		if allocations := testing.AllocsPerRun(100, func() { c, err := OpenMultirange(ctx, m.Usage(), r.Usage(), value); if err != nil { panic(err) }; for { _, ok, err := c.Next(ctx); if err != nil { panic(err) }; if !ok { break } } }); allocations != 0 { t.Fatal("cursor allocation", allocations) }
	}
}
