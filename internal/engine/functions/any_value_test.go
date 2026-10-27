package functions

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
		sqltype.Regrole:    {"0", "4294967295"},
		sqltype.Xid:        {"0", "4294967295"},
		sqltype.Cid:        {"0", "4294967295"},
		sqltype.Xid8:       {"0", "9223372036854775808", "18446744073709551615"},
		sqltype.PgLSN:      {"0/0", "0/1", "80000000/0", "FFFFFFFF/FFFFFFFF"},
		sqltype.PgSnapshot: {"10:10:", "12:20:13,15,18", "1:18446744073709551615:3"},
		sqltype.Refcursor:  {"", "portal name", "é🙂"},
		2275:               {"", "abc", "é🙂", "tail  "},
		sqltype.Tid:        {"(0,0)", "(1,2)", "(4294967295,65535)", "(-1,0)"},
		sqltype.Macaddr:    {"00:00:00:00:00:00", "08002b010203", "80:00:00:00:00:00", "ff:ff:ff:ff:ff:ff"},
		sqltype.Box:        {"(1,4),(3,2)", "(1.0000005,1),(0,0)", "(-0,-0),(-0,-0)", "(5e-324,1),(0,0)", "(NaN,Infinity),(-Infinity,0)"},
		sqltype.Lseg:       {"[(1,4),(3,2)]", "[(3,2),(1,4)]", "[(0,0),(1.0000005,0)]", "[(-0,-0),(-0,-0)]", "[(5e-324,1),(0,0)]", "[(NaN,Infinity),(-Infinity,0)]"},
		sqltype.Point:      {"(1,2)", "(1.0000005,2)", "(-0,-0)", "(5e-324,0)", "(NaN,Infinity)"},
		sqltype.Circle:     {"<(1,2),3>", "<(-0,0),-0>", "<(NaN,Infinity),NaN>"},
		sqltype.Line:       {"{1,2,3}", "{2,4,6}", "{1,-0,-0}", "{1,2,5e-324}", "{NaN,Infinity,0}"},
		sqltype.Path:       {"[(1,2),(3,4)]", "((1,2),(3,4))", "[(1,2),(1,2)]", "[(-0,0.10000000000000002)]", "[(5e-324,-5e-324)]", "[(NaN,Infinity),(-Infinity,0)]"},
		sqltype.Polygon:    {"((0,0),(4,0),(0,4))", "((4,0),(0,4),(0,0))", "((0,0),(4,0),(0,4),(0,0))", "((0.0000005,0))", "((-0,5e-324),(0.10000000000000002,Infinity))", "((NaN,-Infinity))", "((-NaN,0))", "((0,-NaN))", "((-NaN,-NaN))"},
		sqltype.Money:      {"-92233720368547758.08", "92233720368547758.07", "0", "1.235"},
		sqltype.Date:       {"2000-02-29", "infinity"}, sqltype.Time: {"24:00:00", "12:34:56.123456"}, sqltype.Timetz: {"12:34:56.123456+02"},
		sqltype.Timestamp: {"2000-02-29 12:34:56.123456", "-infinity"}, sqltype.Timestamptz: {"2000-02-29 12:34:56.123456+02", "infinity"},
		sqltype.Interval: {"1 mon -2 days 03:04:05.006", "24 hours"}, sqltype.Numeric: {"1.2300", "NaN", "Infinity", "-Infinity"},
		sqltype.UUID: {"01234567-89ab-cdef-0123-456789abcdef"}, sqltype.Regclass: {"0", "42"},
		sqltype.JSON:     {"null", ` {"a":1,"a":2} `},
		sqltype.JSONB:    {"null", ` {"a":1,"a":2} `},
		sqltype.JSONPath: {"$", `strict $.a ? (@ > 1)`},
		sqltype.TSVector: {"", "a:3B,1D,3A a:2c", "'a''b' 'space word'"},
		sqltype.TSQuery:  {"", "a & (b & c)", "a | (b | c)", "'a''b':*ac <2> !!z"},
		sqltype.Bit:      {"", "0010"}, sqltype.VarBit: {"", "00001111"},
		sqltype.Int4Range:        {"[1,4]", "empty", "[,]", "[-2147483648,2147483647)"},
		sqltype.Int8Range:        {"(1,4]", "empty", "[,]", "[-9223372036854775808,9223372036854775807)"},
		sqltype.NumRange:         {"(4,5)", "[1.0,1.00]", "empty", "[,]", "[-Infinity,Infinity]", "[Infinity,NaN]", "[NaN,NaN]", "[9007199254740992.0000000000000001,9007199254740992.0000000000000002]"},
		sqltype.DateRange:        {"[2000-02-28,2000-03-01]", "empty", "[,]", "[-infinity,infinity]", "[infinity,infinity]"},
		sqltype.TimestampRange:   {"(2000-01-01 00:00:00,2000-01-01 00:00:00.000001)", "[epoch,epoch]", "empty", "[,]", "[-infinity,infinity]", "(infinity,)", "(,-infinity)"},
		sqltype.TimestamptzRange: {"(2000-01-01 00:00:00+00,2000-01-01 00:00:00.000001+00)", "[epoch,epoch]", "empty", "[,]", "[-infinity,infinity]", "(infinity,)", "(,-infinity)"},
		sqltype.Int4Multirange:   {"{}", "{(,)}", "{[1,3),[2,4),[8,9)}"},
		sqltype.Int8Multirange:   {"{}", "{(,)}", "{[-9223372036854775808,9223372036854775807)}"},
		sqltype.NumMultirange:    {"{}", "{(4,5)}", "{[1.0,1.00]}", "{[-Infinity,Infinity],[Infinity,NaN]}"},
		13246:                    {"", "tail  ", "é🙂"},
		13248:                    {"", "MiXeDé", "O'Brien\\tail\n"},
		13254:                    {"2004-10-19 10:23:54.126+02", "2004-10-19 23:59:59.999+00", "infinity", "-infinity"},
		sqltype.Inet:             {"10.255.255.255/8", "::ffff:1.2.3.4", "::1"},
		sqltype.Cidr:             {"10", "192.168/25", "2001:db8::/113"},
		sqltype.Regcollation:     {"0", "4294967295"},
		sqltype.Regnamespace:     {"0", "4294967295"},
		sqltype.Macaddr8:         {"00:00:00:00:00:00:00:00", "08002b010203", "08:00:2b:01:02:03:04:05", "80:00:00:00:00:00:00:00", "ff:ff:ff:ff:ff:ff:ff:ff"},
		sqltype.Int2Vector:       {"", "1 2 3", "-32768 32767"},
		sqltype.OidVector:        {"", "1 2 3", "-1 -2147483648", "0 4294967295", "17 2281", "2281 2281"},
	}
	for _, d := range sqltype.Builtins() {
		if !d.Concrete() {
			continue
		}
		t.Run(d.Name(), func(t *testing.T) {
			var values []sqltype.Value
			if d.IsArray() && !d.IsCatalogVector() {
				var element sqltype.Value
				var err error
				if d.Element().ID() == sqltype.Regtype {
					element, err = sqltype.RegtypeValue(d.Element(), 42)
				} else if sqltype.IsRoutineAlias(d.Element().ID()) {
					element, err = sqltype.RegprocValue(d.Element(), 1242)
				} else if sqltype.IsRegTS(d.Element().ID()) {
					element, err = sqltype.RegTSValue(d.Element(), 42)
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
				case sqltype.PgNDistinct, sqltype.PgDependencies, sqltype.PgMCVList:
					values = append(values, anyValueStatisticsDatum(t, d))
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
				case sqltype.Regproc, sqltype.Regprocedure:
					for _, id := range []uint32{0, math.MaxUint32} {
						value, err := sqltype.RegprocValue(d, id)
						if err != nil {
							t.Fatal(err)
						}
						values = append(values, value)
					}
				case sqltype.Regoper, sqltype.Regoperator:
					for _, id := range []uint32{0, math.MaxUint32} {
						value, err := sqltype.OperatorValue(d, id)
						if err != nil {
							t.Fatal(err)
						}
						values = append(values, value)
					}
				case sqltype.Regconfig, sqltype.Regdictionary:
					for _, id := range []uint32{0, math.MaxUint32} {
						value, err := sqltype.RegTSValue(d, id)
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
				case 13243:
					// As with yes_or_no, retain valid already-checked inline
					// datums without installing an aggregate-owned evaluator.
					for _, number := range []int64{0, 7, math.MaxInt32} {
						physical, err := sqltype.Integer(d.DomainBase().Type, number)
						if err != nil {
							t.Fatal(err)
						}
						value, err := d.Wrap(physical)
						if err != nil {
							t.Fatal(err)
						}
						unwrapped, err := value.BaseValue()
						if err != nil || value.Type() != d || !sqltype.SameDatum(unwrapped, physical) {
							t.Fatal("checked-domain carrier", value, physical, err)
						}
						values = append(values, value)
					}
				case 13256:
					// Aggregates retain already-validated datums; the engine owns
					// CHECK evaluation. Build valid inline carriers without a fake
					// evaluator or bypass in the domain's input implementation.
					for _, text := range []string{"YES", "NO"} {
						physical := sqltype.String(d.DomainBase().Type, text)
						value, err := d.Wrap(physical)
						if err != nil {
							t.Fatal(err)
						}
						unwrapped, err := value.BaseValue()
						if err != nil || value.Type() != d || !sqltype.SameDatum(unwrapped, physical) {
							t.Fatal("checked-domain carrier", value, physical, err)
						}
						values = append(values, value)
					}
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
			if d.ID() == sqltype.Box {
				point, err := d.SubscriptElement().Input(ctx, "(-5,-6)", -1)
				if err != nil {
					t.Fatal(err)
				}
				raw, err := sqltype.ArraySetElement(ctx, values[0], []sqltype.ArraySubscriptValue{{Upper: 0, HasUpper: true}}, point)
				if err != nil {
					t.Fatal(err)
				}
				// A real component write retains an inverted pair. ANY_VALUE
				// must carry it without normalization or codec reconstruction.
				if out, err := d.Output(ctx, raw); err != nil || out != "(-5,-6),(1,2)" {
					t.Fatal("raw BOX carrier construction", out, err)
				}
				values = append(values, raw)
			}
			for _, want := range values {
				p := AnyValue{d.Usage()}
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
				if err := p.Advance(ctx, state, []sqltype.Value{sqltype.Null(d), sqltype.Value{}}, nil); err != nil {
					t.Fatal(err)
				}
				if err := p.Advance(ctx, state, []sqltype.Value{want, sqltype.Value{}}, nil); err != nil {
					t.Fatal(err)
				}
				if err := p.Advance(ctx, state, []sqltype.Value{sqltype.Null(d), sqltype.Value{}}, nil); err != nil {
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
				if err := p.Advance(ctx, state, []sqltype.Value{want, sqltype.Value{}}, nil); err != nil {
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
	p := AnyValue{typ.Usage()}
	state, err := p.Init(ctx)
	if err != nil {
		t.Fatal(err)
	}
	first, _ := sqltype.Integer(typ, 41)
	second, _ := sqltype.Integer(typ, 73)
	for _, v := range []sqltype.Value{first, second, sqltype.Null(typ)} {
		if err := p.Advance(ctx, state, []sqltype.Value{v, sqltype.Value{}}, nil); err != nil {
			t.Fatal(err)
		}
	}
	got, err := p.Finalize(ctx, state)
	if err != nil || !sqltype.SameDatum(got, first) {
		t.Fatal("private retention algorithm", got, err)
	}
	for _, invalid := range []any{nil, struct{}{}, (*anyValueState)(nil), &anyValueState{}, &anyValueState{sqltype.Null(foreign)}} {
		if err := p.Advance(ctx, invalid, []sqltype.Value{first, sqltype.Value{}}, nil); err == nil {
			t.Fatal("invalid state accepted", invalid)
		}
		if _, err := p.Finalize(ctx, invalid); err == nil {
			t.Fatal("invalid finalized state accepted", invalid)
		}
	}
	foreignValue, _ := sqltype.Integer(foreign, 9)
	for _, invalid := range []sqltype.Value{{}, sqltype.Null(foreign), foreignValue} {
		if err := p.Advance(ctx, state, []sqltype.Value{invalid, sqltype.Value{}}, nil); err == nil {
			t.Fatal("foreign/invalid candidate accepted after retention")
		}
	}
	if _, err := (AnyValue{}).Init(ctx); err == nil {
		t.Fatal("invalid result descriptor accepted")
	}
	canceled, cancel := context.WithCancel(ctx)
	cancel()
	if s, err := p.Init(canceled); s != nil || err != context.Canceled {
		t.Fatal(s, err)
	}
	before := state.(*anyValueState).value
	if err := p.Advance(canceled, state, []sqltype.Value{second, sqltype.Value{}}, nil); err != context.Canceled || !sqltype.SameDatum(before, state.(*anyValueState).value) {
		t.Fatal("canceled transition mutated state", err)
	}
	if _, err := p.Finalize(canceled, state); err != context.Canceled {
		t.Fatal(err)
	}
}
