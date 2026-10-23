package functions

import (
	"context"
	"stuntgres/internal/sqltype"
	"testing"
)

func extremaUnitProvider(t *testing.T, d *sqltype.Descriptor, maximum bool) ExtremaProvider {
	t.Helper()
	comparison, err := sqltype.BindComparison(d, d, d.Usage().Collation, false)
	if err != nil {
		t.Fatal(err)
	}
	return ExtremaProvider{usage: d.Usage(), comparison: comparison, maximum: maximum}
}

func TestExtremaProviderFamiliesAndTies(t *testing.T) {
	ctx := context.Background()
	cases := map[sqltype.ID][]string{
		sqltype.Int2: {"32767", "-32768"}, sqltype.Int4: {"2147483647", "-2147483648"}, sqltype.Int8: {"9223372036854775807", "-9223372036854775808"},
		sqltype.Oid: {"4294967295", "0"}, sqltype.Numeric: {"NaN", "-Infinity"}, sqltype.Float4: {"NaN", "-Infinity"}, sqltype.Float8: {"NaN", "-Infinity"},
		sqltype.Text: {"é", "Z"}, sqltype.BPChar: {"z  ", "a "}, sqltype.Bytea: {`\xff`, `\x00`},
		sqltype.Date: {"infinity", "-infinity"}, sqltype.Time: {"24:00:00", "00:00:00"}, sqltype.Timetz: {"08:00:00+00", "09:00:00+01"},
		sqltype.Timestamp: {"infinity", "-infinity"}, sqltype.Timestamptz: {"infinity", "-infinity"}, sqltype.Interval: {"31 days", "-1 day"},
	}
	for _, d := range sqltype.Builtins() {
		inputs, ok := cases[d.ID()]
		if !ok {
			continue
		}
		t.Run(d.Name(), func(t *testing.T) {
			large, err := d.Input(ctx, inputs[0], -1)
			if err != nil {
				t.Fatal(err)
			}
			small, err := d.Input(ctx, inputs[1], -1)
			if err != nil {
				t.Fatal(err)
			}
			for _, maximum := range []bool{false, true} {
				p := extremaUnitProvider(t, d, maximum)
				state, err := p.Init(ctx)
				if err != nil {
					t.Fatal(err)
				}
				isolated, err := p.Init(ctx)
				if err != nil || state == isolated {
					t.Fatal("shared state", err)
				}
				initial, err := p.Finalize(ctx, state)
				if err != nil || initial.Type() != d || !initial.IsNull() {
					t.Fatal(initial, err)
				}
				for _, v := range []sqltype.Value{sqltype.Null(d), large, sqltype.Null(d), small, large, small} {
					if err := p.Advance(ctx, state, []sqltype.Value{v, sqltype.Value{}}, nil); err != nil {
						t.Fatal(err)
					}
				}
				want := small
				if maximum {
					want = large
				}
				for range 2 {
					got, err := p.Finalize(ctx, state)
					if err != nil || !sqltype.SameDatum(got, want) {
						t.Fatal("retained payload", got, want, err)
					}
				}
				got, err := p.Finalize(ctx, isolated)
				if err != nil || !got.IsNull() {
					t.Fatal("execution/group leak", got, err)
				}
			}
		})
	}
	for _, d := range sqltype.Builtins() {
		var inputs []string
		switch d.ID() {
		case sqltype.Numeric:
			inputs = []string{"1.0", "1.00"}
		case sqltype.Interval:
			inputs = []string{"1 mon", "30 days"}
		case sqltype.BPChar:
			inputs = []string{"a", "a "}
		case sqltype.Float4, sqltype.Float8:
			inputs = []string{"0", "-0"}
		default:
			continue
		}
		for _, maximum := range []bool{false, true} {
			for _, reverse := range []bool{false, true} {
				first, second := inputs[0], inputs[1]
				if reverse {
					first, second = second, first
				}
				a, err := d.Input(ctx, first, -1)
				if err != nil {
					t.Fatal(err)
				}
				b, err := d.Input(ctx, second, -1)
				if err != nil {
					t.Fatal(err)
				}
				p := extremaUnitProvider(t, d, maximum)
				s, _ := p.Init(ctx)
				if err := p.Advance(ctx, s, []sqltype.Value{a, sqltype.Value{}}, nil); err != nil {
					t.Fatal(err)
				}
				if err := p.Advance(ctx, s, []sqltype.Value{b, sqltype.Value{}}, nil); err != nil {
					t.Fatal(err)
				}
				want := b
				if d.ID() == sqltype.BPChar {
					want = a
				}
				got, err := p.Finalize(ctx, s)
				if err != nil || !sqltype.SameDatum(got, want) {
					t.Fatal("equal representation", d.Name(), maximum, reverse, got, want, err)
				}
			}
		}
	}
}

func TestExtremaProviderDemandAndInvariants(t *testing.T) {
	ctx := context.Background()
	var typ, foreign *sqltype.Descriptor
	for _, d := range sqltype.Builtins() {
		if d.ID() == sqltype.Text {
			typ = d
		}
	}
	for _, d := range sqltype.Builtins() {
		if d.ID() == sqltype.Text {
			foreign = d
		}
	}
	p := extremaUnitProvider(t, typ, false)
	var bindErr error
	p.comparison, bindErr = sqltype.BindComparison(typ, typ, typ.Usage().Collation, true)
	if bindErr != nil {
		t.Fatal(bindErr)
	}
	s, _ := p.Init(ctx)
	first := sqltype.String(typ, "a")
	for _, v := range []sqltype.Value{sqltype.Null(typ), first, sqltype.Null(typ)} {
		if err := p.Advance(ctx, s, []sqltype.Value{v, sqltype.Value{}}, nil); err != nil {
			t.Fatal("premature comparison", err)
		}
	}
	aggregateUnitState(t, p.Advance(ctx, s, []sqltype.Value{sqltype.String(typ, "b"), sqltype.Value{}}, nil), "42P22")
	got, err := p.Finalize(ctx, s)
	if err != nil || !sqltype.SameDatum(got, first) {
		t.Fatal(got, err)
	}
	for _, invalid := range []any{nil, struct{}{}, (*extremaState)(nil), &extremaState{}, &extremaState{value: sqltype.Null(foreign)}} {
		if err := p.Advance(ctx, invalid, []sqltype.Value{first, sqltype.Value{}}, nil); err == nil {
			t.Fatal("invalid state", invalid)
		}
		if _, err := p.Finalize(ctx, invalid); err == nil {
			t.Fatal("invalid final state", invalid)
		}
	}
	for _, invalid := range []sqltype.Value{{}, sqltype.Null(foreign), sqltype.String(foreign, "x")} {
		if err := p.Advance(ctx, s, []sqltype.Value{invalid, sqltype.Value{}}, nil); err == nil {
			t.Fatal("foreign candidate accepted")
		}
	}
	if _, err := (ExtremaProvider{}).Init(ctx); err == nil {
		t.Fatal("invalid descriptor")
	}
	canceled, cancel := context.WithCancel(ctx)
	cancel()
	if s, err := p.Init(canceled); s != nil || err != context.Canceled {
		t.Fatal(s, err)
	}
	if err := p.Advance(canceled, s, []sqltype.Value{sqltype.String(typ, "0"), sqltype.Value{}}, nil); err != context.Canceled {
		t.Fatal(err)
	}
	if _, err := p.Finalize(canceled, s); err != context.Canceled {
		t.Fatal(err)
	}
	if !sqltype.SameDatum(s.(*extremaState).value, first) {
		t.Fatal("canceled mutation")
	}
}
