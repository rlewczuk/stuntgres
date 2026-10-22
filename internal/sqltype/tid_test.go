package sqltype

import (
	"context"
	"errors"
	"strings"
	"testing"

	"stuntgres/internal/sqlstate"
)

func TestTIDCodec(t *testing.T) {
	ctx := context.Background()
	d := testTypes()[Tid]
	for _, tc := range [][2]string{
		{"(0,0)", "(0,0)"}, {"(0,1)", "(0,1)"}, {"(4294967295,65535)", "(4294967295,65535)"},
		{"(-1,0)", "(4294967295,0)"}, {"(-2147483648,0)", "(2147483648,0)"}, {"(+0001, +0002)", "(1,2)"},
		{" (1,2)junk", "(1,2)"}, {"junk(1,2)", "(1,2)"}, {",1,2)", "(1,2)"}, {"1,2,3)", "(2,3)"},
		{"(,1)", "(0,1)"}, {"(1,)", "(1,0)"}, {"(,)", "(0,0)"},
		{"(18446744073709551615,1)", "(4294967295,1)"}, {"(-18446744073709551615,-18446744073709551615)", "(1,1)"},
	} {
		v, err := d.Input(ctx, tc[0], -1)
		if err != nil {
			t.Fatalf("%q: %v", tc[0], err)
		}
		out, err := d.Output(ctx, v)
		if err != nil || out != tc[1] {
			t.Fatalf("%q: got %q %v want %q", tc[0], out, err, tc[1])
		}
		reparsed, err := d.Input(ctx, tc[1], -1)
		if err != nil || !SameDatum(v, reparsed) {
			t.Fatal("canonical round trip", tc, err)
		}
	}
	for _, ws := range []string{" ", "\t", "\n", "\v", "\f", "\r"} {
		v, err := d.Input(ctx, "("+ws+"1,"+ws+"2)", -1)
		if err != nil {
			t.Fatal(err)
		}
		block, offset, err := v.TID()
		if err != nil || block != 1 || offset != 2 {
			t.Fatal(block, offset, err)
		}
	}
	for _, input := range []string{"", "bad", "(1)", "(1,2", "(1,2,3)", "(0x1,2)", "(4294967296,1)", "(-2147483649,1)", "(1,65536)", "(1,-1)", "(18446744073709551616,1)", "(1 ,2)", "(1,2 )", "( ,1)", "(1, )", "bad\"\\\n"} {
		_, err := d.Input(ctx, input, -1)
		var diagnostic *sqlstate.Error
		if !errors.As(err, &diagnostic) || diagnostic.Code != "22P02" || diagnostic.Message != `invalid input syntax for type tid: "`+input+`"` || diagnostic.Position != "" || diagnostic.Context != "" || diagnostic.Detail != "" || diagnostic.Hint != "" {
			t.Fatalf("%q: %+v", input, err)
		}
	}
}

func TestTIDAuthorityAndOrdering(t *testing.T) {
	ctx := context.Background()
	types := testTypes()
	d := types[Tid]
	if _, err := TIDValue(nil, 0, 0); err == nil {
		t.Fatal("nil owner accepted")
	}
	if _, err := TIDValue(types[Oid], 0, 0); err == nil {
		t.Fatal("OID owner accepted")
	}
	for _, v := range []Value{{}, Null(d), {typ: types[Oid]}, {typ: d, integer: -1}, {typ: d, integer: 1 << 48}} {
		if _, _, err := v.TID(); err == nil {
			t.Fatal("invalid accessor value", v)
		}
	}
	var values []Value
	for _, pair := range [][2]uint32{{0, 0}, {0, 1}, {1, 2}, {1, 10}, {2, 0}, {2147483647, 65535}, {2147483648, 0}, {4294967295, 65535}} {
		v, err := TIDValue(d, pair[0], uint16(pair[1]))
		if err != nil {
			t.Fatal(err)
		}
		block, offset, err := v.TID()
		if err != nil || block != pair[0] || uint32(offset) != pair[1] {
			t.Fatal(block, offset, err)
		}
		values = append(values, v)
	}
	comparison, err := BindOrdering(d.Usage())
	if err != nil {
		t.Fatal(err)
	}
	for i, a := range values {
		for j, b := range values {
			want := 0
			if i < j {
				want = -1
			}
			if i > j {
				want = 1
			}
			got, err := comparison.Compare(ctx, a, b, InputEnvironment{})
			if err != nil || got != want {
				t.Fatal(i, j, got, want, err)
			}
		}
	}
	other := testTypes()[Tid]
	foreign, _ := TIDValue(other, 1, 2)
	if _, err := d.Output(ctx, foreign); err == nil {
		t.Fatal("foreign output owner accepted")
	}
	if _, err := comparison.Compare(ctx, values[0], foreign, InputEnvironment{}); err == nil {
		t.Fatal("foreign compare owner accepted")
	}
	alias, _ := d.Input(ctx, "(-1,65535)", -1)
	if !SameDatum(alias, values[len(values)-1]) {
		t.Fatal("canonical alias identity")
	}
	g, err := BindGrouping(d.Usage())
	if err != nil {
		t.Fatal(err)
	}
	ha, err := g.Hash(ctx, alias)
	if err != nil {
		t.Fatal(err)
	}
	hb, err := g.Hash(ctx, values[len(values)-1])
	if err != nil || ha != hb {
		t.Fatal("alias hash", ha, hb, err)
	}
	if equal, err := g.Equal(ctx, Null(d), Null(d)); err != nil || !equal {
		t.Fatal("grouped NULL", equal, err)
	}
	for op := CompareEqual; op <= CompareGreaterEqual; op++ {
		predicate, err := BindComparisonPredicate(op, d, d, types[Bool], CollationRef{}, false)
		if err != nil {
			t.Fatal(err)
		}
		value, err := predicate.Evaluate(ctx, Null(d), values[0], InputEnvironment{})
		if err != nil || !value.IsNull() || value.Type() != types[Bool] {
			t.Fatal("strict NULL", value, err)
		}
	}
	for _, method := range []bool{false, true} {
		c, ok := LookupIndexClass("tid_ops")
		if method {
			c, ok = LookupHashIndexClass("tid_ops")
		}
		if !ok || !c.Accepts(d) {
			t.Fatal(c)
		}
		if method {
			provider, err := bindTestIndexHash(d.Usage(), c)
			if err != nil {
				t.Fatal(err)
			}
			if equal, err := provider.Equal(ctx, alias, values[len(values)-1]); err != nil || !equal {
				t.Fatal(equal, err)
			}
		} else {
			if _, err := bindTestIndexOrdering(d.Usage(), c); err != nil {
				t.Fatal(err)
			}
		}
	}
}

func TestTIDCancellationAndCoercion(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	types := testTypes()
	d := types[Tid]
	v, _ := TIDValue(d, 1, 2)
	if _, err := d.Input(ctx, strings.Repeat("x", 8192)+"(1,2)", -1); !errors.Is(err, context.Canceled) {
		t.Fatal(err)
	}
	if _, err := d.Output(ctx, v); !errors.Is(err, context.Canceled) {
		t.Fatal(err)
	}
	comparison, _ := BindOrdering(d.Usage())
	if _, err := comparison.Compare(ctx, v, v, InputEnvironment{}); !errors.Is(err, context.Canceled) {
		t.Fatal(err)
	}
	for _, pair := range [][2]ID{{Int4, Tid}, {Tid, Int4}, {Bool, Tid}, {Tid, Oid}} {
		if _, err := PlanCoercion(types[pair[0]].Usage(), types[pair[1]].Usage(), Explicit); err == nil {
			t.Fatal("unrelated coercion", pair)
		}
	}
	if _, err := PlanCoercion(types[Text].Usage(), d.Usage(), Implicit); err == nil {
		t.Fatal("implicit text conversion")
	}
	for _, pair := range [][2]ID{{Text, Tid}, {Tid, Text}, {VarChar, Tid}, {Tid, VarChar}} {
		if _, err := PlanCoercion(types[pair[0]].Usage(), types[pair[1]].Usage(), Explicit); err != nil {
			t.Fatal(pair, err)
		}
	}
	if types[1010].Concrete() {
		t.Fatal("TID array became executable")
	}
}
