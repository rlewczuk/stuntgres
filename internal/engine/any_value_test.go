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
	aclAuthority := &sqltype.ACLAuthority{}
	inputs := map[sqltype.ID][]string{
		sqltype.Bool: {"false", "true"}, sqltype.Bytea: {`\x`, `\x00ff5c`}, sqltype.Char: {"x", "é"}, sqltype.Name: {"", "MiXeDé"},
		sqltype.Int2: {"-32768", "32767"}, sqltype.Int4: {"-2147483648", "2147483647"}, sqltype.Int8: {"-9223372036854775808", "9223372036854775807"}, sqltype.Text: {"", "x'y"},
		sqltype.Oid: {"0", "4294967295"}, sqltype.BPChar: {"x   ", "    "}, sqltype.VarChar: {"", "xy"}, sqltype.Float4: {"-0", "NaN", "Infinity"}, sqltype.Float8: {"-0", "NaN", "-Infinity"},
		sqltype.Regrole: {"0", "4294967295"},
		sqltype.Xid:     {"0", "4294967295"},
		sqltype.Cid:     {"0", "4294967295"},
		sqltype.Xid8:    {"0", "9223372036854775808", "18446744073709551615"},
		sqltype.Money:   {"-92233720368547758.08", "92233720368547758.07", "0", "1.235"},
		sqltype.Date:    {"2000-02-29", "infinity"}, sqltype.Time: {"24:00:00", "12:34:56.123456"}, sqltype.Timetz: {"12:34:56.123456+02"},
		sqltype.Timestamp: {"2000-02-29 12:34:56.123456", "-infinity"}, sqltype.Timestamptz: {"2000-02-29 12:34:56.123456+02", "infinity"},
		sqltype.Interval: {"1 mon -2 days 03:04:05.006", "24 hours"}, sqltype.Numeric: {"1.2300", "NaN", "Infinity", "-Infinity"},
		sqltype.UUID: {"01234567-89ab-cdef-0123-456789abcdef"}, sqltype.Regclass: {"0", "42"},
		sqltype.JSON:     {"null", ` {"a":1,"a":2} `},
		sqltype.JSONB:    {"null", ` {"a":1,"a":2} `},
		sqltype.JSONPath: {"$", `strict $.a ? (@ > 1)`},
		sqltype.Bit:      {"", "0010"}, sqltype.VarBit: {"", "00001111"},
		sqltype.Int4Range:      {"[1,4]", "empty", "[,]", "[-2147483648,2147483647)"},
		sqltype.Int8Range:      {"(1,4]", "empty", "[,]", "[-9223372036854775808,9223372036854775807)"},
		sqltype.NumRange:       {"(4,5)", "[1.0,1.00]", "empty", "[,]", "[-Infinity,Infinity]", "[Infinity,NaN]", "[NaN,NaN]", "[9007199254740992.0000000000000001,9007199254740992.0000000000000002]"},
		sqltype.DateRange:      {"[2000-02-28,2000-03-01]", "empty", "[,]", "[-infinity,infinity]", "[infinity,infinity]"},
		sqltype.Int4Multirange: {"{}", "{(,)}", "{[1,3),[2,4),[8,9)}"},
		sqltype.Int8Multirange: {"{}", "{(,)}", "{[-9223372036854775808,9223372036854775807)}"},
		sqltype.NumMultirange:  {"{}", "{(4,5)}", "{[1.0,1.00]}", "{[-Infinity,Infinity],[Infinity,NaN]}"},
		13254:                  {"2004-10-19 10:23:54.126+02", "2004-10-19 23:59:59.999+00", "infinity", "-infinity"},
		sqltype.Inet:           {"10.255.255.255/8", "::ffff:1.2.3.4", "::1"},
		sqltype.Cidr:           {"10", "192.168/25", "2001:db8::/113"},
	}
	for _, d := range sqltype.Builtins() {
		if !d.Concrete() {
			continue
		}
		t.Run(d.Name(), func(t *testing.T) {
			var values []sqltype.Value
			if d.IsArray() {
				var element sqltype.Value
				var err error
				if d.Element().ID() == sqltype.Regtype {
					element, err = sqltype.RegtypeValue(d.Element(), 42)
				} else if d.Element().ID() == sqltype.ACLItem {
					element, err = sqltype.ACLItemValue(d.Element(), aclAuthority, 20, 30, sqltype.ACLPrivilegeMask, sqltype.ACLPrivilegeMask)
				} else {
					element, err = d.Element().Input(ctx, inputs[d.Element().ID()][0], -1)
				}
				if err != nil {
					t.Fatal(err)
				}
				value, err := sqltype.ArrayValue(d, []sqltype.ArrayDimension{{Lower: 1, Length: 2}}, []sqltype.Value{element, sqltype.Null(d.Element())})
				if err != nil {
					t.Fatal(err)
				}
				values = append(values, value)
			} else {
				switch d.ID() {
				case sqltype.ACLItem:
					for _, grantee := range []uint32{0, 20} {
						value, err := sqltype.ACLItemValue(d, aclAuthority, grantee, 30, sqltype.ACLPrivilegeMask, sqltype.ACLPrivilegeMask)
						if err != nil {
							t.Fatal(err)
						}
						values = append(values, value)
					}
				case sqltype.Regtype:
					for _, id := range []uint32{0, math.MaxUint32} {
						value, err := sqltype.RegtypeValue(d, id)
						if err != nil {
							t.Fatal(err)
						}
						values = append(values, value)
					}
				case sqltype.Regproc:
					for _, id := range []uint32{0, math.MaxUint32} {
						value, err := sqltype.RegprocValue(d, id)
						if err != nil {
							t.Fatal(err)
						}
						values = append(values, value)
					}
				case sqltype.Void:
					values = append(values, sqltype.VoidValue(d))
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
						if d.IsDomain() {
							base := d.Usage().BaseUsage()
							physical, err := base.Type.Input(ctx, text, base.Typmod)
							if err != nil {
								t.Fatal("domain base witness", text, err)
							}
							unwrapped, err := v.BaseValue()
							if err != nil || v.Type() != d || unwrapped.Type() != base.Type || !sqltype.SameDatum(unwrapped, physical) {
								t.Fatal("domain tag/base datum", v, physical, err)
							}
						}
						values = append(values, v)
					}
					if len(values) == 0 {
						t.Fatalf("missing concrete carrier witness %s", d.Name())
					}
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
					// VOID has no structural datum identity; its checked carrier
					// still survives aggregation without reconstruction.
					same := sqltype.SameDatum(got, want)
					if d.ID() == sqltype.Void {
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
