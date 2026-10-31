package core_test

import (
	"context"
	"fmt"
	"testing"

	"stuntgres/internal/engine"
	"stuntgres/internal/engine/analyze"
	"stuntgres/internal/engine/expr"
	"stuntgres/internal/engine/plan"
	"stuntgres/internal/engine/stored"
	"stuntgres/internal/sqltype"
)

func TestDeparseConstraintsLiteralTypes(t *testing.T) {
	e := engine.New()
	view := e.CatalogRead()
	for _, tc := range []struct {
		id                     sqltype.ID
		input, compact, pretty string
	}{
		{sqltype.Bool, "true", "true", "true"},
		{sqltype.Int2, "1", "'1'::smallint", "'1'::smallint"},
		{sqltype.Int4, "1", "1", "1"},
		{sqltype.Int8, "1", "'1'::bigint", "'1'::bigint"},
		{sqltype.Oid, "1", "'1'::oid", "'1'::oid"},
		{sqltype.Text, "hello", "'hello'::text", "'hello'::text"},
		{sqltype.Name, "hello", "'hello'::name", "'hello'::name"},
		{sqltype.Char, "x", "'x'::\"char\"", "'x'::\"char\""},
		{sqltype.BPChar, "hi", "'hi'::bpchar", "'hi'::bpchar"},
		{sqltype.VarChar, "hi", "'hi'::character varying", "'hi'::character varying"},
		{sqltype.Bytea, `\x00ff`, `'\x00ff'::bytea`, `'\x00ff'::bytea`},
		{sqltype.UUID, "00000000-0000-0000-0000-000000000001", "'00000000-0000-0000-0000-000000000001'::uuid", "'00000000-0000-0000-0000-000000000001'::uuid"},
		{sqltype.Inet, "192.168.0.129/25", "'192.168.0.129/25'::inet", "'192.168.0.129/25'::inet"},
		{sqltype.Cidr, "10", "'10.0.0.0/8'::cidr", "'10.0.0.0/8'::cidr"},
		{sqltype.Numeric, "1.20", "1.20", "1.20"},
		{sqltype.Numeric, "NaN", "'NaN'::numeric", "'NaN'::numeric"},
		{sqltype.Numeric, "Infinity", "'Infinity'::numeric", "'Infinity'::numeric"},
		{sqltype.Numeric, "-Infinity", "'-Infinity'::numeric", "'-Infinity'::numeric"},
		{sqltype.Float4, "1.5", "'1.5'::real", "'1.5'::real"},
		{sqltype.Float8, "NaN", "'NaN'::double precision", "'NaN'::double precision"},
		{sqltype.Float8, "Infinity", "'Infinity'::double precision", "'Infinity'::double precision"},
		{sqltype.Float8, "-Infinity", "'-Infinity'::double precision", "'-Infinity'::double precision"},
		{sqltype.Date, "2026-10-08", "'2026-10-08'::date", "'2026-10-08'::date"},
		{sqltype.Time, "12:34:56", "'12:34:56'::time without time zone", "'12:34:56'::time without time zone"},
		{sqltype.Timetz, "12:34:56+00", "'12:34:56+00'::time with time zone", "'12:34:56+00'::time with time zone"},
		{sqltype.Timestamp, "2026-10-08 12:34:56", "'2026-10-08 12:34:56'::timestamp without time zone", "'2026-10-08 12:34:56'::timestamp without time zone"},
		{sqltype.Timestamptz, "2026-10-08 12:34:56+00", "'2026-10-08 12:34:56+00'::timestamp with time zone", "'2026-10-08 12:34:56+00'::timestamp with time zone"},
		{sqltype.Interval, "1 day", "'1 day'::interval", "'1 day'::interval"},
		{sqltype.Regclass, "123", "'123'::regclass", "'123'::regclass"},
	} {
		t.Run(fmt.Sprintf("%d/%s", tc.id, tc.input), func(t *testing.T) {
			typ, ok := view.LookupType(tc.id)
			if !ok {
				t.Fatal(tc.id)
			}
			value, err := typ.Input(context.Background(), tc.input, -1)
			if err != nil {
				t.Fatal(err)
			}
			node := plan.LiteralExpr{Info: expr.Info{Usage: typ.Usage()}, Value: value}
			for _, pretty := range []bool{false, true} {
				got, err := (stored.Deparser{View: view, Pretty: pretty}).SQL(context.Background(), node, stored.DeparseParent{})
				want := tc.compact
				if pretty {
					want = tc.pretty
				}
				if err != nil || got != want {
					t.Fatalf("got %q, %v; want %q", got, err, want)
				}
				node.Value = sqltype.Null(typ)
				got, err = (stored.Deparser{View: view, Pretty: pretty}).SQL(context.Background(), node, stored.DeparseParent{})
				declaration, err2 := analyze.PGType(typ.Usage())
				if err != nil || err2 != nil || got != "NULL::"+declaration {
					t.Fatal(got, err, err2)
				}
				node.Value = value
			}
		})
	}
}

func TestDeparseConstraintsTypmodsAndLiteralVariants(t *testing.T) {
	e := engine.New()
	view := e.CatalogRead()
	for _, tc := range []struct {
		id              sqltype.ID
		input, expected string
		modifiers       []int32
	}{
		{sqltype.Int4, "-1", "'-1'::integer", nil},
		{sqltype.Numeric, "1", "'1'::numeric", nil},
		{sqltype.Numeric, "-1.20", "'-1.20'::numeric", nil},
		{sqltype.Numeric, "1.20", "1.20::numeric(4,2)", []int32{4, 2}},
		{sqltype.BPChar, "xy", "'xy '::character(3)", []int32{3}},
		{sqltype.VarChar, "xy", "'xy'::character varying(3)", []int32{3}},
		{sqltype.Time, "12:34:56.123", "'12:34:56.123'::time(3) without time zone", []int32{3}},
		{sqltype.Timestamp, "2026-10-08 12:34:56.123", "'2026-10-08 12:34:56.123'::timestamp(3) without time zone", []int32{3}},
		{sqltype.Timetz, "12:34:56.123+00", "'12:34:56.123+00'::time(3) with time zone", []int32{3}},
		{sqltype.Timestamptz, "2026-10-08 12:34:56.123+00", "'2026-10-08 12:34:56.123+00'::timestamp(3) with time zone", []int32{3}},
		{sqltype.Date, "infinity", "'infinity'::date", nil},
		{sqltype.Date, "-infinity", "'-infinity'::date", nil},
		{sqltype.Timestamp, "infinity", "'infinity'::timestamp without time zone", nil},
		{sqltype.Timestamp, "-infinity", "'-infinity'::timestamp without time zone", nil},
		{sqltype.Timestamptz, "infinity", "'infinity'::timestamp with time zone", nil},
		{sqltype.Timestamptz, "-infinity", "'-infinity'::timestamp with time zone", nil},
		{sqltype.Float4, "NaN", "'NaN'::real", nil},
		{sqltype.Float4, "Infinity", "'Infinity'::real", nil},
		{sqltype.Float4, "-Infinity", "'-Infinity'::real", nil},
		{sqltype.Float8, "1.5", "'1.5'::double precision", nil},
	} {
		t.Run(fmt.Sprintf("%d/%s/%v", tc.id, tc.input, tc.modifiers), func(t *testing.T) {
			typ, _ := view.LookupType(tc.id)
			u := typ.Usage()
			if tc.modifiers != nil {
				var err error
				u, _, err = typ.ResolveModifiers(tc.modifiers...)
				if err != nil {
					t.Fatal(err)
				}
			}
			value, err := typ.Input(context.Background(), tc.input, u.Typmod)
			if err != nil {
				t.Fatal(err)
			}
			node := plan.LiteralExpr{Info: expr.Info{Usage: u}, Value: value}
			for _, pretty := range []bool{false, true} {
				got, err := (stored.Deparser{View: view, Pretty: pretty}).SQL(context.Background(), node, stored.DeparseParent{})
				if err != nil || got != tc.expected {
					t.Fatal(got, err, tc.expected)
				}
			}
		})
	}
}
