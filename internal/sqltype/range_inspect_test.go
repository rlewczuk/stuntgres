package sqltype

import (
	"context"
	"errors"
	"slices"
	"testing"
)

func inspectUnitTypes() map[ID]*Descriptor {
	m := map[ID]*Descriptor{}
	for _, d := range Builtins() {
		m[d.ID()] = d
	}
	return m
}
func inspectUnitInput(t *testing.T, d *Descriptor, text string) Value {
	t.Helper()
	v, err := d.Input(context.Background(), text, -1)
	if err != nil {
		t.Fatal(err)
	}
	return v
}
func inspectUnitOutput(t *testing.T, v Value, want string) {
	t.Helper()
	text, err := v.typ.Output(context.Background(), v)
	if err != nil || text != want {
		t.Fatalf("output=%q want=%q err=%v", text, want, err)
	}
}
func TestRangeInspectPlanMatrix(t *testing.T) {
	types := inspectUnitTypes()
	ctx := context.Background()
	for _, tc := range []struct {
		id            ID
		input, lo, hi string
		li, ui        bool
	}{
		{Int4Range, "[1,4]", "1", "5", true, false}, {Int8Range, "(1,4]", "2", "5", true, false}, {NumRange, "(1.00,4.0]", "1.00", "4.0", false, true},
		{DateRange, "[2000-02-29,2000-02-29]", "2000-02-29", "2000-03-01", true, false},
		{TimestampRange, "[2000-02-29 12:34:56,2000-02-29 12:34:56]", "2000-02-29 12:34:56", "2000-02-29 12:34:56", true, true},
		{TimestamptzRange, "[2000-02-29 12:34:56+00,2000-02-29 12:34:56+00]", "2000-02-29 12:34:56+00", "2000-02-29 12:34:56+00", true, true},
	} {
		d := types[tc.id]
		finite := inspectUnitInput(t, d, tc.input)
		for _, op := range []RangeInspectOperation{RangeLower, RangeUpper, RangeIsEmpty, RangeLowerInclusive, RangeUpperInclusive, RangeLowerInfinite, RangeUpperInfinite} {
			result := RangeInspectResult(op, d)
			p, err := BindRangeInspect(op, result.Usage(), d.Usage())
			if err != nil {
				t.Fatal(err)
			}
			if p.Name() != op.Name() || p.ResultUsage() != result.Usage() {
				t.Fatal("plan metadata")
			}
			got, err := p.Evaluate(ctx, [2]Value{finite})
			if err != nil || got.Type() != result || got.IsNull() {
				t.Fatal(got, err)
			}
			switch op {
			case RangeLower:
				inspectUnitOutput(t, got, tc.lo)
			case RangeUpper:
				inspectUnitOutput(t, got, tc.hi)
			default:
				want := false
				if op == RangeLowerInclusive {
					want = tc.li
				}
				if op == RangeUpperInclusive {
					want = tc.ui
				}
				if got.boolean != want {
					t.Fatal("canonical flags", op, got)
				}
			}
			null, err := p.Evaluate(ctx, [2]Value{Null(d)})
			if err != nil || !null.IsNull() || null.Type() != result {
				t.Fatal("typed null", null, err)
			}
			for _, input := range []string{"empty", "(,)"} {
				value := inspectUnitInput(t, d, input)
				got, err := p.Evaluate(ctx, [2]Value{value})
				if err != nil {
					t.Fatal(err)
				}
				if op == RangeLower || op == RangeUpper {
					if !got.IsNull() {
						t.Fatal("missing bound")
					}
				} else {
					want := input == "empty" && op == RangeIsEmpty || input == "(,)" && (op == RangeLowerInfinite || op == RangeUpperInfinite)
					if got.boolean != want {
						t.Fatal("empty/omission flags", op, input, got)
					}
				}
			}
			if allocations := testing.AllocsPerRun(100, func() {
				if _, err := p.Evaluate(ctx, [2]Value{finite}); err != nil {
					panic(err)
				}
			}); allocations != 0 {
				t.Fatal("scalar allocations", op, allocations)
			}
		}
	}
}
func TestRangeInspectAuthorityAndCancellation(t *testing.T) {
	types, fresh := inspectUnitTypes(), inspectUnitTypes()
	ctx := context.Background()
	for _, id := range []ID{Int4Range, Int8Range, NumRange, DateRange, TimestampRange, TimestamptzRange, Int4Multirange, Int8Multirange, NumMultirange} {
		d := types[id]
		for _, op := range []RangeInspectOperation{RangeLower, RangeUpper, RangeIsEmpty, RangeLowerInclusive, RangeUpperInclusive, RangeLowerInfinite, RangeUpperInfinite, RangeMerge} {
			result := RangeInspectResult(op, d)
			args := []Usage{d.Usage()}
			if op == RangeMerge && isRange(id) {
				args = append(args, d.Usage())
			}
			p, err := BindRangeInspect(op, result.Usage(), args...)
			if err != nil {
				t.Fatal(id, op, err)
			}
			if _, err := BindRangeInspect(op, fresh[result.ID()].Usage(), args...); err == nil {
				t.Fatal("foreign result", id, op)
			}
			foreign := append([]Usage(nil), args...)
			foreign[0] = fresh[id].Usage()
			if _, err := BindRangeInspect(op, result.Usage(), foreign...); err == nil {
				t.Fatal("foreign argument", id, op)
			}
			if _, err := BindRangeInspect(op, result.Usage()); err == nil {
				t.Fatal("zero arity")
			}
			values := [2]Value{Null(d), Null(d)}
			values[0] = Null(fresh[id])
			if _, err := p.Evaluate(ctx, values); err == nil {
				t.Fatal("foreign value")
			}
			canceled, cancel := context.WithCancel(ctx)
			cancel()
			if _, err := p.Evaluate(canceled, [2]Value{Null(d), Null(d)}); !errors.Is(err, context.Canceled) {
				t.Fatal(err)
			}
		}
	}
	if _, err := BindRangeInspect(RangeMerge, types[Int4Range].Usage(), types[Int4Range].Usage(), types[Int8Range].Usage()); err == nil {
		t.Fatal("mixed family")
	}
	if _, err := BindRangeInspect(RangeMerge, types[Int4Range].Usage(), types[Int4Range].Usage()); err == nil {
		t.Fatal("unary range hull")
	}
	if _, err := BindRangeInspect(RangeMerge, types[Int4Range].Usage(), types[Int4Multirange].Usage(), types[Int4Multirange].Usage()); err == nil {
		t.Fatal("binary multirange hull")
	}
}
func TestRangeInspectHullAndSealedStorage(t *testing.T) {
	types := inspectUnitTypes()
	ctx := context.Background()
	for _, tc := range []struct {
		id      ID
		a, b, w string
	}{
		{Int4Range, "[1,2)", "[3,4)", "[1,4)"}, {Int8Range, "empty", "[1,4]", "[1,5)"},
		{NumRange, "(1,2)", "[1,3]", "[1,3]"}, {NumRange, "[1.0,2.00]", "[1.000,2.0]", "[1.000,2.0]"},
		{NumRange, "[1.000,2.0]", "[1.0,2.00]", "[1.0,2.00]"}, {DateRange, "[-infinity,infinity]", "(,)", "(,)"},
	} {
		d := types[tc.id]
		a, b := inspectUnitInput(t, d, tc.a), inspectUnitInput(t, d, tc.b)
		p, err := BindRangeInspect(RangeMerge, d.Usage(), d.Usage(), d.Usage())
		if err != nil {
			t.Fatal(err)
		}
		got, err := p.Evaluate(ctx, [2]Value{a, b})
		if err != nil {
			t.Fatal(err)
		}
		inspectUnitOutput(t, got, tc.w)
		if a != inspectUnitInput(t, d, tc.a) || b != inspectUnitInput(t, d, tc.b) {
			t.Fatal("input mutation")
		}
		if allocations := testing.AllocsPerRun(100, func() {
			if _, err := p.Evaluate(ctx, [2]Value{a, b}); err != nil {
				panic(err)
			}
		}); allocations != 0 {
			t.Fatal("hull allocations", allocations)
		}
	}
	for _, id := range []ID{Int4Multirange, Int8Multirange, NumMultirange} {
		d := types[id]
		v := inspectUnitInput(t, d, "{[8,9),empty,[1,3),[2,4)}")
		before := slices.Clone(v.multirange.members)
		for op := RangeLower; op <= RangeMerge; op++ {
			result := RangeInspectResult(op, d)
			p, err := BindRangeInspect(op, result.Usage(), d.Usage())
			if err != nil {
				t.Fatal(err)
			}
			got, err := p.Evaluate(ctx, [2]Value{v})
			if err != nil {
				t.Fatal(err)
			}
			if op == RangeMerge {
				inspectUnitOutput(t, got, "[1,9)")
			}
			if !slices.Equal(before, v.multirange.members) {
				t.Fatal("sealed storage mutation")
			}
			if allocations := testing.AllocsPerRun(100, func() {
				if _, err := p.Evaluate(ctx, [2]Value{v}); err != nil {
					panic(err)
				}
			}); allocations != 0 {
				t.Fatal("multirange inspection allocations", allocations)
			}
		}
	}
}
