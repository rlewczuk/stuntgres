package sqltype

import (
	"context"
	"errors"
	"strconv"
	"testing"

	"stuntgres/internal/sqlstate"
)

func TestOIDCodec(t *testing.T) {
	d := testTypes()[Oid]
	ctx := context.Background()
	for _, tc := range []struct {
		input string
		want  uint32
	}{
		{"0", 0}, {"1", 1}, {"2147483647", 2147483647}, {"2147483648", 2147483648}, {"4294967295", 4294967295},
		{"-1", 4294967295}, {"-2147483648", 2147483648}, {"-1040", 4294966256}, {"0xffffffff", 4294967295}, {"0377", 255}, {"-18446744073709551615", 1}, {"\t\n\r\v\f +0007 \t", 7},
		{"18446744073709551615", 4294967295}, {"18446744071562067968", 2147483648},
	} {
		v, err := d.Input(ctx, tc.input, -1)
		if err != nil {
			t.Fatal(tc, err)
		}
		n, err := v.OID()
		if err != nil || n != tc.want {
			t.Fatal(tc, n, err)
		}
		want := strconv.FormatUint(uint64(tc.want), 10)
		out, err := d.Output(ctx, v)
		if err != nil || out != want {
			t.Fatal(tc, out, err)
		}
		literal, err := d.SQLLiteral(ctx, v)
		if err != nil || literal != "'"+want+"'" {
			t.Fatal(tc, literal, err)
		}
	}
	for _, tc := range []struct{ input, code string }{
		{"", "22P02"}, {" \t\r\v\f\n", "22P02"}, {"+", "22P02"}, {"-", "22P02"}, {"+ 1", "22P02"}, {"- 1", "22P02"}, {"x", "22P02"}, {"08", "22P02"}, {"0x", "22P02"}, {"0b10", "22P02"}, {"0o10", "22P02"}, {"1_000", "22P02"}, {"\u00a01", "22P02"}, {"1\u00a0", "22P02"}, {"4294967296x", "22P02"},
		{"4294967296", "22003"}, {"-2147483649", "22003"}, {"18446744073709551616x", "22003"}, {"-18446744073709551616", "22003"},
		{"18446744071562067967", "22003"},
		{"18446744073709551616", "22003"},
	} {
		_, err := d.Input(ctx, tc.input, -1)
		var e *sqlstate.Error
		message := "invalid input syntax for type oid: \"" + tc.input + "\""
		if tc.code == "22003" {
			message = "value \"" + tc.input + "\" is out of range for type oid"
		}
		if !errors.As(err, &e) || e.Code != tc.code || e.Message != message || e.Position != "" {
			t.Fatal(tc, err)
		}
	}
	if _, err := OIDValue(testTypes()[Int4], 1); err == nil {
		t.Fatal("foreign constructor")
	}
	if _, err := Null(d).OID(); err == nil {
		t.Fatal("NULL accessor")
	}
	if _, err := (Value{}).OID(); err == nil {
		t.Fatal("invalid accessor")
	}
	if _, err := d.Output(ctx, Null(d)); err == nil {
		t.Fatal("NULL output")
	}
	if out, err := d.SQLLiteral(ctx, Null(d)); err != nil || out != "NULL" {
		t.Fatal(out, err)
	}
	cancelled, cancel := context.WithCancel(ctx)
	cancel()
	if _, err := d.Input(cancelled, "1", -1); !errors.Is(err, context.Canceled) {
		t.Fatal(err)
	}
}

func TestOIDCasts(t *testing.T) {
	ts := testTypes()
	ctx := context.Background()
	for _, tc := range []struct {
		from, to    ID
		input, want string
		minimum     CoercionContext
	}{
		{Int2, Oid, "-1", "4294967295", Implicit}, {Int2, Oid, "-32768", "4294934528", Implicit},
		{Int4, Oid, "-1", "4294967295", Implicit}, {Int4, Oid, "-2147483648", "2147483648", Implicit},
		{Int8, Oid, "0", "0", Implicit}, {Int8, Oid, "4294967295", "4294967295", Implicit},
		{Oid, Int4, "4294967295", "-1", Assignment}, {Oid, Int4, "2147483648", "-2147483648", Assignment},
		{Oid, Int8, "4294967295", "4294967295", Assignment}, {Oid, Int8, "0", "0", Assignment},
		{Text, Oid, "-1", "4294967295", Explicit}, {Oid, Text, "4294967295", "4294967295", Assignment}, {Oid, Oid, "1", "1", Implicit},
	} {
		for mode := Implicit; mode <= Explicit; mode++ {
			p, err := PlanCoercion(ts[tc.from].Usage(), ts[tc.to].Usage(), mode)
			if mode < tc.minimum {
				if err == nil {
					t.Fatal(tc, mode)
				}
				continue
			}
			if err != nil {
				t.Fatal(tc, mode, err)
			}
			v, err := ts[tc.from].Input(ctx, tc.input, -1)
			if err != nil {
				t.Fatal(err)
			}
			out, err := p.Apply(ctx, v, InputEnvironment{}, OutputEnvironment{})
			if err != nil {
				t.Fatal(tc, err)
			}
			s, err := ts[tc.to].Output(ctx, out)
			if err != nil || s != tc.want {
				t.Fatal(tc, s, err)
			}
			null, err := p.Apply(ctx, Null(ts[tc.from]), InputEnvironment{}, OutputEnvironment{})
			if err != nil || !null.IsNull() || null.Type() != ts[tc.to] {
				t.Fatal(tc, null, err)
			}
		}
	}
	for _, input := range []string{"-1", "4294967296"} {
		v, _ := ts[Int8].Input(ctx, input, -1)
		p, _ := PlanCoercion(ts[Int8].Usage(), ts[Oid].Usage(), Implicit)
		_, err := p.Apply(ctx, v, InputEnvironment{}, OutputEnvironment{})
		var e *sqlstate.Error
		if !errors.As(err, &e) || e.Code != "22003" || e.Message != "OID out of range" || e.Position != "" {
			t.Fatal(err)
		}
	}
	for mode := Implicit; mode <= Explicit; mode++ {
		if _, err := PlanCoercion(ts[Oid].Usage(), ts[Int2].Usage(), mode); err == nil {
			t.Fatal("transitive cast")
		}
	}
	if _, err := PlanCoercion(ts[Int2].Usage(), ts[Text].Usage(), Explicit); err != nil {
		t.Fatal("released integer output I/O refused", err)
	}
}

func TestOIDComparisonGrouping(t *testing.T) {
	ts := testTypes()
	ctx := context.Background()
	d := ts[Oid]
	values := []uint32{0, 1, 2147483647, 2147483648, 4294967295}
	g, err := BindGrouping(d.Usage())
	if err != nil {
		t.Fatal(err)
	}
	for i, a := range values {
		for j, b := range values {
			av, _ := OIDValue(d, a)
			bv, _ := OIDValue(d, b)
			for op := CompareEqual; op <= CompareGreaterEqual; op++ {
				p, err := BindComparisonPredicate(op, d, d, ts[Bool], CollationRef{}, false)
				if err != nil {
					t.Fatal(err)
				}
				v, err := p.Evaluate(ctx, av, bv, InputEnvironment{})
				if err != nil {
					t.Fatal(err)
				}
				got, _ := v.Boolean()
				want := []bool{i == j, i != j, i < j, i <= j, i > j, i >= j}[op]
				if got != want {
					t.Fatal(i, j, op, got)
				}
				n, err := p.Evaluate(ctx, av, Null(d), InputEnvironment{})
				if err != nil || !n.IsNull() || n.Type() != ts[Bool] {
					t.Fatal(n, err)
				}
			}
			equal, err := g.Equal(ctx, av, bv)
			if err != nil || equal != (i == j) || SameDatum(av, bv) != equal {
				t.Fatal(i, j, err)
			}
			if equal {
				ah, _ := g.Hash(ctx, av)
				bh, _ := g.Hash(ctx, bv)
				if ah != bh {
					t.Fatal("hash")
				}
			}
		}
	}
	v, _ := OIDValue(d, 1)
	integer, _ := Integer(ts[Int4], 1)
	if SameDatum(v, integer) {
		t.Fatal("identity")
	}
	cancelled, cancel := context.WithCancel(ctx)
	cancel()
	if _, err := g.Hash(cancelled, v); !errors.Is(err, context.Canceled) {
		t.Fatal(err)
	}
}
