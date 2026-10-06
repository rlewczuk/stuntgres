package sqltype

import (
	"context"
	"errors"
	"strconv"
	"testing"

	"stuntgres/internal/sqlstate"
)

func TestSmallintIdentityAndInput(t *testing.T) {
	ctx := context.Background()
	d := testTypes()[Int2]
	if d.ID() != 21 || d.Name() != "int2" || d.SQLName() != "SMALLINT" || d.DatabaseName() != "INT2" || !d.Usage().Valid() {
		t.Fatal(d)
	}
	if literal, err := d.SQLLiteral(ctx, Null(d)); err != nil || literal != "NULL" {
		t.Fatal(literal, err)
	}
	for _, tc := range []struct {
		text string
		n    int64
	}{
		{"-32768", -32768}, {"32767", 32767}, {" +0007 ", 7}, {"\t\n\r\f\v-8\t", -8}, {"010", 10}, {"08", 8}, {"0x_FF", 255}, {"0o17", 15}, {"0b10_10", 10}, {"1_000", 1000}, {"-0X_10", -16}, {"-0", 0}, {"-0x8000", -32768}, {"0x7fff", 32767},
	} {
		v, err := d.Input(ctx, tc.text, -1)
		if err != nil {
			t.Fatal(tc, err)
		}
		n, err := v.Integer()
		if err != nil || n != tc.n || v.Type() != d {
			t.Fatal(tc, n, err)
		}
		for _, output := range []func(context.Context, Value) (string, error){d.Output, d.SQLLiteral} {
			s, err := output(ctx, v)
			if err != nil || s != strconv.FormatInt(tc.n, 10) {
				t.Fatal(s, err)
			}
		}
		if _, err := testTypes()[Int2].Output(ctx, v); err == nil {
			t.Fatal("foreign output accepted")
		}
	}
	for _, text := range []string{"", "+", "-", "_1", "1_", "1__0", "0x", "0x_", "0b2", "1.2", "1e2", "+ 1", "\u00a01", "32768x", "0x8000x"} {
		_, err := d.Input(ctx, text, -1)
		smallintError(t, err, "22P02", "invalid input syntax for type smallint: \""+text+"\"")
	}
	for _, text := range []string{"32768", "-32769", "327680x", "0x80000x"} {
		_, err := d.Input(ctx, text, -1)
		smallintError(t, err, "22003", "value \""+text+"\" is out of range for type smallint")
	}
	for _, n := range []int64{-32769, 32768} {
		v, err := Integer(d, n)
		smallintError(t, err, "22003", "smallint out of range")
		if v.Type() != nil {
			t.Fatal("invalid value published")
		}
	}
	if _, err := Integer(testTypes()[Text], 1); err == nil {
		t.Fatal("wrong descriptor accepted")
	}
}

func smallintError(t *testing.T, err error, code, message string) {
	t.Helper()
	var got *sqlstate.Error
	want := sqlstate.Error{Severity: "ERROR", SeverityUnlocalized: "ERROR", Code: code, Message: message}
	if !errors.As(err, &got) || *got != want {
		t.Fatalf("got %#v (%v), want %#v", got, err, want)
	}
}

func TestSmallintCoercionAndComparison(t *testing.T) {
	ctx := context.Background()
	types := testTypes()
	d := types[Int2]
	v, _ := Integer(d, 32767)
	for _, id := range []ID{Int4, Int8, Float4, Float8, Text} {
		mode := Implicit
		if id == Text {
			mode = Assignment
		}
		p, err := PlanCoercion(d.Usage(), types[id].Usage(), mode)
		if err != nil {
			t.Fatal(id, err)
		}
		got, err := p.Apply(ctx, v)
		if err != nil || got.Type() != types[id] {
			t.Fatal(got, err)
		}
		s, err := types[id].Output(ctx, got)
		if err != nil || s != "32767" {
			t.Fatal(s, err)
		}
		null, err := p.Apply(ctx, Null(d))
		if err != nil || !null.IsNull() || null.Type() != types[id] {
			t.Fatal(null, err)
		}
	}
	for _, id := range []ID{Int4, Int8} {
		if _, err := PlanCoercion(types[id].Usage(), d.Usage(), Implicit); err == nil {
			t.Fatal("implicit narrowing admitted")
		}
		p, err := PlanCoercion(types[id].Usage(), d.Usage(), Assignment)
		if err != nil {
			t.Fatal(err)
		}
		for _, n := range []int64{-32768, 32767} {
			wide, _ := Integer(types[id], n)
			got, err := p.Apply(ctx, wide)
			if err != nil || got.Type() != d {
				t.Fatal(got, err)
			}
			actual, err := got.Integer()
			if err != nil || actual != n {
				t.Fatal(actual, err)
			}
		}
		null, err := p.Apply(ctx, Null(types[id]))
		if err != nil || !null.IsNull() || null.Type() != d {
			t.Fatal(null, err)
		}
		for _, n := range []int64{-32769, 32768} {
			wide, _ := Integer(types[id], n)
			got, err := p.Apply(ctx, wide)
			smallintError(t, err, "22003", "smallint out of range")
			if got.Type() != nil {
				t.Fatal(got)
			}
		}
	}
	for _, id := range []ID{Int2, Int4, Int8, Float4, Float8} {
		var other Value
		if isFloat(id) {
			other, _ = Float(types[id], 32768)
		} else {
			n := int64(32767)
			if id != Int2 {
				n = 32768
			}
			other, _ = Integer(types[id], n)
		}
		for _, reverse := range []bool{false, true} {
			a, b := v, other
			want := -1
			if id == Int2 {
				want = 0
			}
			if reverse {
				a, b = b, a
				want = -want
			}
			p, err := BindComparison(a.Type(), b.Type())
			if err != nil {
				t.Fatal(err)
			}
			n, err := p.Compare(ctx, a, b)
			if err != nil || n != want {
				t.Fatal(id, n, err)
			}
			eq, err := BindComparisonPredicate(CompareEqual, a.Type(), b.Type(), types[Bool])
			if err != nil {
				t.Fatal(err)
			}
			null, err := eq.Evaluate(ctx, Null(a.Type()), b)
			if err != nil || !null.IsNull() {
				t.Fatal(null, err)
			}
		}
	}
	min, _ := Integer(d, -32768)
	max, _ := Integer(types[Int8], 9223372036854775807)
	p, _ := BindComparison(d, types[Int8])
	if n, err := p.Compare(ctx, min, max); err != nil || n != -1 {
		t.Fatal(n, err)
	}
}
