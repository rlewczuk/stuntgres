package engine

import (
	"context"
	"errors"
	"math"
	"testing"

	"stuntgres/internal/sqltype"
)

type anyValueUnitTree struct{}

func (anyValueUnitTree) StructuralOutput(context.Context) (string, error) {
	return "", errors.New("retention must not serialize structural data")
}

func TestAnyValueProviderDatums(t *testing.T) {
	ctx := context.Background()
	inputs := map[sqltype.ID][]string{
		sqltype.Bool: {"false", "true"}, sqltype.Bytea: {`\x`, `\x00ff5c`}, sqltype.Char: {"x", "é"}, sqltype.Name: {"", "MiXeDé"},
		sqltype.Int2: {"-32768", "32767"}, sqltype.Int4: {"-2147483648", "2147483647"}, sqltype.Int8: {"-9223372036854775808", "9223372036854775807"}, sqltype.Text: {"", "x'y"},
		sqltype.Oid: {"0", "4294967295"}, sqltype.BPChar: {"x   ", "    "}, sqltype.VarChar: {"", "xy"}, sqltype.Float4: {"-0", "NaN", "Infinity"}, sqltype.Float8: {"-0", "NaN", "-Infinity"},
		sqltype.Date: {"2000-02-29", "infinity"}, sqltype.Time: {"24:00:00", "12:34:56.123456"}, sqltype.Timetz: {"12:34:56.123456+02"},
		sqltype.Timestamp: {"2000-02-29 12:34:56.123456", "-infinity"}, sqltype.Timestamptz: {"2000-02-29 12:34:56.123456+02", "infinity"},
		sqltype.Interval: {"1 mon -2 days 03:04:05.006", "24 hours"}, sqltype.Numeric: {"1.2300", "NaN", "Infinity", "-Infinity"},
		sqltype.UUID: {"01234567-89ab-cdef-0123-456789abcdef"}, sqltype.Regclass: {"0", "42"},
	}
	for _, d := range sqltype.Builtins() {
		if !d.Concrete() {
			continue
		}
		t.Run(d.Name(), func(t *testing.T) {
			var values []sqltype.Value
			switch d.ID() {
			case sqltype.Void:
				values = append(values, sqltype.VoidValue(d))
			case sqltype.NameArray:
				v, err := sqltype.Names(d, []string{"public", "MiXeDé"})
				if err != nil {
					t.Fatal(err)
				}
				values = append(values, v)
			case sqltype.PgNodeTree:
				v, err := sqltype.Tree(d, anyValueUnitTree{})
				if err != nil {
					t.Fatal(err)
				}
				values = append(values, v)
			default:
				for _, text := range inputs[d.ID()] {
					v, err := d.Input(ctx, text, -1)
					if err != nil {
						t.Fatal(text, err)
					}
					values = append(values, v)
				}
				if len(values) == 0 {
					t.Fatalf("missing concrete carrier witness %s", d.Name())
				}
			}
			if d.ID() == sqltype.Float8 {
				v, err := sqltype.Float(d, math.Float64frombits(0x7ff8000000000042))
				if err != nil {
					t.Fatal(err)
				}
				values = append(values, v)
			}
			for _, want := range values {
				p := anyValueProvider{d.Usage()}
				state, err := p.Init(ctx)
				if err != nil {
					t.Fatal(err)
				}
				isolated, err := p.Init(ctx)
				if err != nil {
					t.Fatal(err)
				}
				if state == isolated {
					t.Fatal("states shared across groups/executions")
				}
				for range 2 {
					got, err := p.Finalize(ctx, state)
					if err != nil || !got.IsNull() || got.Type() != d {
						t.Fatal("initial typed NULL", got, err)
					}
				}
				if err := p.Advance(ctx, state, sqltype.Null(d), sqltype.Value{}); err != nil {
					t.Fatal(err)
				}
				if err := p.Advance(ctx, state, want, sqltype.Value{}); err != nil {
					t.Fatal(err)
				}
				if err := p.Advance(ctx, state, sqltype.Null(d), sqltype.Value{}); err != nil {
					t.Fatal(err)
				}
				for range 2 {
					got, err := p.Finalize(ctx, state)
					// SameDatum deliberately excludes VOID/NAME[] expression
					// identity; exact comparable carriers still retain all payload.
					same := sqltype.SameDatum(got, want)
					if d.ID() == sqltype.Void || d.ID() == sqltype.NameArray {
						same = got == want
					}
					if err != nil || !same {
						t.Fatal("datum reconstructed or consumed", got, want, err)
					}
				}
				got, err := p.Finalize(ctx, isolated)
				if err != nil || !got.IsNull() || got.Type() != d {
					t.Fatal("group/execution leaked", got, err)
				}
				if err := p.Advance(ctx, state, want, sqltype.Value{}); err != nil {
					t.Fatal(err)
				}
			}
		})
	}
}

func TestAnyValueProviderStateAndCancellation(t *testing.T) {
	ctx := context.Background()
	var typ, foreign *sqltype.Descriptor
	for _, d := range sqltype.Builtins() {
		if d.ID() == sqltype.Int4 {
			typ = d
		}
	}
	for _, d := range sqltype.Builtins() {
		if d.ID() == sqltype.Int4 {
			foreign = d
		}
	}
	p := anyValueProvider{typ.Usage()}
	state, err := p.Init(ctx)
	if err != nil {
		t.Fatal(err)
	}
	first, _ := sqltype.Integer(typ, 41)
	second, _ := sqltype.Integer(typ, 73)
	for _, v := range []sqltype.Value{first, second, sqltype.Null(typ)} {
		if err := p.Advance(ctx, state, v, sqltype.Value{}); err != nil {
			t.Fatal(err)
		}
	}
	got, err := p.Finalize(ctx, state)
	if err != nil || !sqltype.SameDatum(got, first) {
		t.Fatal("private retention algorithm", got, err)
	}
	for _, invalid := range []any{nil, struct{}{}, (*anyValueState)(nil), &anyValueState{}, &anyValueState{sqltype.Null(foreign)}} {
		if err := p.Advance(ctx, invalid, first, sqltype.Value{}); err == nil {
			t.Fatal("invalid state accepted", invalid)
		}
		if _, err := p.Finalize(ctx, invalid); err == nil {
			t.Fatal("invalid finalized state accepted", invalid)
		}
	}
	foreignValue, _ := sqltype.Integer(foreign, 9)
	for _, invalid := range []sqltype.Value{{}, sqltype.Null(foreign), foreignValue} {
		if err := p.Advance(ctx, state, invalid, sqltype.Value{}); err == nil {
			t.Fatal("foreign/invalid candidate accepted after retention")
		}
	}
	if _, err := (anyValueProvider{}).Init(ctx); err == nil {
		t.Fatal("invalid result descriptor accepted")
	}
	canceled, cancel := context.WithCancel(ctx)
	cancel()
	if s, err := p.Init(canceled); s != nil || err != context.Canceled {
		t.Fatal(s, err)
	}
	before := state.(*anyValueState).value
	if err := p.Advance(canceled, state, second, sqltype.Value{}); err != context.Canceled || !sqltype.SameDatum(before, state.(*anyValueState).value) {
		t.Fatal("canceled transition mutated state", err)
	}
	if _, err := p.Finalize(canceled, state); err != context.Canceled {
		t.Fatal(err)
	}
}

func TestAnyValueSlotsAndMetadata(t *testing.T) {
	_, q, source := queryCoreFixture(t, "SELECT any_value(v),any_value(v),count(v),any_value(f) FROM t")
	defer source.close()
	node := q.Root.(*projectNode).child.(*aggregateNode)
	if len(node.calls) != 3 {
		t.Fatal("aggregate identity sharing", len(node.calls))
	}
	for i, c := range q.Root.Schema() {
		if !c.NullabilityKnown || c.Nullable != (i != 2) || c.OriginTable != 0 || c.OriginAttribute != 0 || c.Usage.Typmod != -1 {
			t.Fatal("result metadata", c)
		}
	}
	_, volatile, owner := queryCoreFixture(t, "SELECT any_value(nextval($1)),any_value(nextval($1)) FROM t")
	defer owner.close()
	calls := volatile.Root.(*projectNode).child.(*aggregateNode).calls
	if len(calls) != 2 {
		t.Fatal("volatile occurrences shared")
	}
	for _, call := range calls {
		if !call.argument.Properties().ParameterDependent || call.argument.Properties().Volatility != Volatile {
			t.Fatal(call.argument.Properties())
		}
	}
}
