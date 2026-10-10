package sqltype

import (
	"context"
	"errors"
	"fmt"
	"math"
	"stuntgres/internal/sqlstate"
	"testing"
)

func rangeUnitInput(t *testing.T, d *Descriptor, text string) Value {
	t.Helper()
	v, err := d.Input(context.Background(), text, -1)
	if err != nil {
		t.Fatal(err)
	}
	return v
}

func TestWindowRangeRegisteredPairs(t *testing.T) {
	d := testTypes()
	lookup := func(id ID) *Descriptor { return d[id] }
	for _, tc := range []struct {
		key               ID
		offsets           []ID
		val, base, offset string
	}{
		{Int2, []ID{Int2, Int4, Int8}, "3", "2", "1"}, {Int4, []ID{Int2, Int4, Int8}, "3", "2", "1"}, {Int8, []ID{Int8}, "3", "2", "1"},
		{Float4, []ID{Float8}, "3", "2", "1"}, {Float8, []ID{Float8}, "3", "2", "1"}, {Numeric, []ID{Numeric}, "3", "2", "1"},
		{Date, []ID{Interval}, "2025-01-03", "2025-01-02", "1 day"}, {Timestamp, []ID{Interval}, "2025-01-03", "2025-01-02", "1 day"},
		{Timestamptz, []ID{Interval}, "2025-01-03 00:00+00", "2025-01-02 00:00+00", "1 day"},
		{Time, []ID{Interval}, "03:00", "02:00", "1 hour"}, {Timetz, []ID{Interval}, "03:00+00", "02:00+00", "1 hour"}, {Interval, []ID{Interval}, "3 days", "2 days", "1 day"},
	} {
		for _, id := range tc.offsets {
			t.Run(fmt.Sprintf("%s_%s", d[tc.key].Name(), d[id].Name()), func(t *testing.T) {
				p, u, err := BindRange(d[tc.key].Usage(), d[id], lookup, (*Descriptor).DiagnosticName)
				if err != nil || u != d[id].Usage() {
					t.Fatal(p, u, err)
				}
				val, base, offset := rangeUnitInput(t, d[tc.key], tc.val), rangeUnitInput(t, d[tc.key], tc.base), rangeUnitInput(t, d[id], tc.offset)
				before := [3]Value{val, base, offset}
				for _, sub := range []bool{false, true} {
					for _, less := range []bool{false, true} {
						want := true
						if sub {
							want = !less
						}
						got, err := p.InRange(context.Background(), val, base, offset, sub, less, InputEnvironment{})
						if err != nil || got != want {
							t.Fatal(sub, less, got, want, err)
						}
					}
				}
				if before != [3]Value{val, base, offset} {
					t.Fatal("mutated operands")
				}
				ctx, cancel := context.WithCancel(context.Background())
				cancel()
				got, err := p.InRange(ctx, val, base, offset, false, true, InputEnvironment{})
				var diagnostic *sqlstate.Error
				negativeText := "-1"
				if id == Interval {
					negativeText = "-1 hour"
				}
				negative := rangeUnitInput(t, d[id], negativeText)
				if got || !errors.Is(err, context.Canceled) || errors.As(err, &diagnostic) {
					t.Fatal(got, err)
				}
				_, err = p.InRange(context.Background(), val, base, negative, false, true, InputEnvironment{})
				want := sqlstate.Error{Severity: "ERROR", SeverityUnlocalized: "ERROR", Code: "22013", Message: "invalid preceding or following size in window function"}
				if !errors.As(err, &diagnostic) || *diagnostic != want {
					t.Fatal(err)
				}
			})
		}
	}
}

func TestWindowRangeSelectionAndRemap(t *testing.T) {
	d := testTypes()
	lookup := func(id ID) *Descriptor { return d[id] }
	for _, tc := range []struct{ key, source, want ID }{{Int2, 0, Int2}, {Int4, 0, Int4}, {Int8, Int2, Int8}, {Float4, Float4, Float8}, {Date, 0, Interval}, {Numeric, Int4, Numeric}} {
		source := d[tc.source]
		p, u, err := BindRange(d[tc.key].Usage(), source, lookup, (*Descriptor).DiagnosticName)
		if err != nil || u.Type != d[tc.want] || u.Typmod != -1 {
			t.Fatal(tc, p, u, err)
		}
	}
	domain, err := Domain(48000, "range_bound", "range_bound", d[Int4].Usage())
	if err != nil {
		t.Fatal(err)
	}
	_, _, err = BindRange(d[Int4].Usage(), domain, lookup, (*Descriptor).DiagnosticName)
	var diagnostic *sqlstate.Error
	if !errors.As(err, &diagnostic) || diagnostic.Code != "0A000" || diagnostic.Message != "RANGE with offset PRECEDING/FOLLOWING has multiple interpretations for column type integer and offset type range_bound" || diagnostic.Hint != "Cast the offset value to the exact intended type." {
		t.Fatal(err)
	}
	for _, id := range []ID{Text, VarChar, BPChar, Name, Char, Bool, Bytea, Money, Oid, Regclass, Regtype, UUID, JSONB} {
		_, _, err := BindRange(d[id].Usage(), d[Int4], lookup, (*Descriptor).DiagnosticName)
		if !errors.As(err, &diagnostic) || diagnostic.Code != "0A000" {
			t.Fatal(id, err)
		}
	}
	p, _, err := BindRange(domain.Usage(), d[Int8], lookup, (*Descriptor).DiagnosticName)
	if err != nil {
		t.Fatal(err)
	}
	fresh := testTypes()
	mapped, err := p.Remap(func(old *Descriptor) *Descriptor {
		if old == nil {
			return nil
		}
		return fresh[old.ID()]
	})
	if err != nil || mapped.key != fresh[Int4] || mapped.offset != fresh[Int8] || mapped.timestamp != fresh[Int4] {
		t.Fatal(mapped, err)
	}
	if mapped.key == p.key || mapped.offset == p.offset {
		t.Fatal("remap retained source authority")
	}
}

func TestWindowRangeIntegerAndClockBoundaries(t *testing.T) {
	d := testTypes()
	lookup := func(id ID) *Descriptor { return d[id] }
	p, _, err := BindRange(d[Int8].Usage(), d[Int8], lookup, (*Descriptor).DiagnosticName)
	if err != nil {
		t.Fatal(err)
	}
	for _, tc := range []struct {
		val, base, offset int64
		sub, less, want   bool
	}{
		{math.MaxInt64, math.MaxInt64 - 1, 4, false, true, true}, {math.MaxInt64, math.MaxInt64 - 1, 4, false, false, false},
		{math.MinInt64, math.MinInt64 + 1, 4, true, false, true}, {math.MinInt64, math.MinInt64 + 1, 4, true, true, false},
	} {
		v, _ := Integer(d[Int8], tc.val)
		b, _ := Integer(d[Int8], tc.base)
		o, _ := Integer(d[Int8], tc.offset)
		got, err := p.InRange(context.Background(), v, b, o, tc.sub, tc.less, InputEnvironment{})
		if err != nil || got != tc.want {
			t.Fatal(tc, got, err)
		}
	}
	for _, id := range []ID{Time, Timetz} {
		p, _, err := BindRange(d[id].Usage(), d[Interval], lookup, (*Descriptor).DiagnosticName)
		if err != nil {
			t.Fatal(err)
		}
		val := Value{typ: d[id], integer: 23*3600000000 + 30*60000000}
		base := Value{typ: d[id], integer: 30 * 60000000}
		offset, _ := IntervalValue(d[Interval], 0, 0, 3600000000)
		got, err := p.InRange(context.Background(), val, base, offset, true, true, InputEnvironment{})
		if got || err != nil {
			t.Fatal(id, got, err)
		}
		offset, _ = IntervalValue(d[Interval], -1, -1, 0)
		got, err = p.InRange(context.Background(), base, base, offset, false, true, InputEnvironment{})
		if !got || err != nil {
			t.Fatal(id, got, err)
		}
		offset, _ = IntervalValue(d[Interval], 0, 0, math.MaxInt64)
		got, err = p.InRange(context.Background(), val, val, offset, false, true, InputEnvironment{})
		if !got || err != nil {
			t.Fatal(id, got, err)
		}
	}
}

func TestWindowRangeSpecialPredicates(t *testing.T) {
	d := testTypes()
	lookup := func(id ID) *Descriptor { return d[id] }
	for _, id := range []ID{Float4, Float8, Numeric} {
		offsetID := Float8
		if id == Numeric {
			offsetID = Numeric
		}
		p, _, err := BindRange(d[id].Usage(), d[offsetID], lookup, (*Descriptor).DiagnosticName)
		if err != nil {
			t.Fatal(err)
		}
		for _, tc := range []struct {
			val, base, offset string
			sub, less, want   bool
		}{
			{"NaN", "NaN", "0", true, true, true}, {"NaN", "0", "0", false, true, false}, {"NaN", "0", "0", false, false, true},
			{"0", "NaN", "0", false, true, true}, {"0", "NaN", "0", false, false, false},
			{"-Infinity", "Infinity", "Infinity", true, true, true}, {"Infinity", "-Infinity", "Infinity", false, false, true},
			{"0", "0", "Infinity", true, true, false}, {"0", "0", "Infinity", true, false, true},
			{"Infinity", "0", "Infinity", false, false, true},
		} {
			got, err := p.InRange(context.Background(), rangeUnitInput(t, d[id], tc.val), rangeUnitInput(t, d[id], tc.base), rangeUnitInput(t, d[offsetID], tc.offset), tc.sub, tc.less, InputEnvironment{})
			if err != nil || got != tc.want {
				t.Fatal(id, tc, got, err)
			}
		}
		for _, text := range []string{"NaN", "-Infinity"} {
			_, err := p.InRange(context.Background(), rangeUnitInput(t, d[id], "0"), rangeUnitInput(t, d[id], "0"), rangeUnitInput(t, d[offsetID], text), true, true, InputEnvironment{})
			var diagnostic *sqlstate.Error
			if !errors.As(err, &diagnostic) || diagnostic.Code != "22013" {
				t.Fatal(id, text, err)
			}
		}
	}
}

func TestWindowRangeTemporalPredicateOverflow(t *testing.T) {
	d := testTypes()
	lookup := func(id ID) *Descriptor { return d[id] }
	for _, tc := range []struct {
		key                              ID
		val, base, offset, code, message string
	}{
		{Timestamp, "2025-01-01", "294276-12-31 23:59:59.999999", "1 microsecond", "22008", "timestamp out of range"},
		{Interval, "0 seconds", "2147483647 months", "1 month", "22008", "interval out of range"},
		{Date, "2025-01-01", "5874897-12-31", "0 days", "22008", "date out of range for timestamp"},
		{Date, "2025-01-01", "5874897-12-31", "-1 day", "22008", "date out of range for timestamp"},
	} {
		p, _, err := BindRange(d[tc.key].Usage(), d[Interval], lookup, (*Descriptor).DiagnosticName)
		if err != nil {
			t.Fatal(err)
		}
		_, err = p.InRange(context.Background(), rangeUnitInput(t, d[tc.key], tc.val), rangeUnitInput(t, d[tc.key], tc.base), rangeUnitInput(t, d[Interval], tc.offset), false, true, InputEnvironment{})
		datetimeArithmeticDiagnostic(t, err, tc.code, tc.message)
	}
	p, _, err := BindRange(d[Float4].Usage(), d[Float8], lookup, (*Descriptor).DiagnosticName)
	if err != nil {
		t.Fatal(err)
	}
	// FLOAT4 keys promote before computing the bound:16777216+1 is16777217,
	// not the FLOAT4-rounded16777216. Both directions differ at the exact key.
	v := rangeUnitInput(t, d[Float4], "16777216")
	offset := rangeUnitInput(t, d[Float8], "1")
	got, err := p.InRange(context.Background(), v, v, offset, false, false, InputEnvironment{})
	if got || err != nil {
		t.Fatal(got, err)
	}
}

func TestWindowRangeVisibleOffsetDiagnosticName(t *testing.T) {
	types := testTypes()
	lookup := func(id ID) *Descriptor { return types[id] }
	for _, base := range []ID{Int4, Text} {
		domain, err := Domain(48001, "range_bound", "public.range_bound", types[base].Usage())
		if err != nil {
			t.Fatal(err)
		}
		for _, name := range []string{"range_bound", `"Hidden schema"."Range Bound"`} {
			_, _, err := BindRange(types[Int4].Usage(), domain, lookup, func(source *Descriptor) string {
				if source != domain {
					t.Fatal("wrong diagnostic source", source)
				}
				return name
			})
			message := "RANGE with offset PRECEDING/FOLLOWING has multiple interpretations for column type integer and offset type " + name
			hint := "Cast the offset value to the exact intended type."
			if base == Text {
				message = "RANGE with offset PRECEDING/FOLLOWING is not supported for column type integer and offset type " + name
				hint = "Cast the offset value to an appropriate type."
			}
			want := sqlstate.Error{Severity: "ERROR", SeverityUnlocalized: "ERROR", Code: "0A000", Message: message, Hint: hint}
			var diagnostic *sqlstate.Error
			if !errors.As(err, &diagnostic) || *diagnostic != want {
				t.Fatal(err, want)
			}
		}
	}
}
