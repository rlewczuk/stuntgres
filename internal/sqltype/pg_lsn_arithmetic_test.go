package sqltype

import (
	"context"
	"errors"
	"testing"
)

func TestPGLSNArithmetic(t *testing.T) {
	types := testTypes()
	ctx := context.Background()
	for _, tc := range []struct{ left, right, want string }{
		{"0/0", "0/0", "0"}, {"0/1", "0/0", "1"}, {"0/0", "0/1", "-1"},
		{"1/0", "0/0", "4294967296"}, {"0/0", "1/0", "-4294967296"},
		{"FFFFFFFF/FFFFFFFF", "0/0", "18446744073709551615"}, {"0/0", "FFFFFFFF/FFFFFFFF", "-18446744073709551615"},
	} {
		p, err := BindArithmetic(ArithmeticSubtract, types[PgLSN], types[PgLSN], types[Numeric])
		if err != nil {
			t.Fatal(err)
		}
		v, err := p.Binary(ctx, pgLSNUnitInput(t, types[PgLSN], tc.left), pgLSNUnitInput(t, types[PgLSN], tc.right), InputEnvironment{})
		if err != nil {
			t.Fatal(err)
		}
		out, err := types[Numeric].Output(ctx, v)
		if err != nil || out != tc.want {
			t.Fatal(tc, out, err)
		}
	}
	for _, tc := range []struct {
		op                      ArithmeticOperator
		lsn, offset, want, code string
	}{
		{ArithmeticAdd, "0/FFFFFFFF", "1", "1/0", ""}, {ArithmeticSubtract, "1/0", "1", "0/FFFFFFFF", ""},
		{ArithmeticAdd, "0/1", "-1", "0/0", ""}, {ArithmeticSubtract, "0/0", "-1", "0/1", ""},
		{ArithmeticAdd, "FFFFFFFF/FFFFFFFF", "-18446744073709551615", "0/0", ""},
		{ArithmeticAdd, "0/0", "18446744073709551615", "FFFFFFFF/FFFFFFFF", ""},
		{ArithmeticAdd, "0/0", "0.49", "0/0", ""}, {ArithmeticAdd, "0/0", "0.5", "0/1", ""},
		{ArithmeticAdd, "0/1", "-0.5", "0/1", ""}, {ArithmeticSubtract, "0/1", "0.5", "0/1", ""},
		{ArithmeticSubtract, "0/0", "0.49", "0/0", ""}, {ArithmeticAdd, "FFFFFFFF/FFFFFFFF", "0.49", "FFFFFFFF/FFFFFFFF", ""},
		{ArithmeticSubtract, "0/0", "0.5", "pg_lsn out of range", "22023"},
		{ArithmeticAdd, "FFFFFFFF/FFFFFFFF", "0.5", "pg_lsn out of range", "22023"},
		{ArithmeticAdd, "FFFFFFFF/FFFFFFFF", "1", "pg_lsn out of range", "22023"},
		{ArithmeticAdd, "0/0", "-1", "pg_lsn out of range", "22023"},
		{ArithmeticSubtract, "0/0", "1", "pg_lsn out of range", "22023"},
		{ArithmeticSubtract, "FFFFFFFF/FFFFFFFF", "-1", "pg_lsn out of range", "22023"},
		{ArithmeticAdd, "0/0", "1e1000", "pg_lsn out of range", "22023"},
		{ArithmeticAdd, "0/0", "1e-1000", "0/0", ""},
		{ArithmeticAdd, "0/0", "NaN", "cannot add NaN to pg_lsn", "0A000"},
		{ArithmeticSubtract, "0/0", "NaN", "cannot subtract NaN from pg_lsn", "0A000"},
		{ArithmeticAdd, "0/0", "Infinity", "cannot convert infinity to pg_lsn", "0A000"},
		{ArithmeticSubtract, "0/0", "Infinity", "cannot convert infinity to pg_lsn", "0A000"},
		{ArithmeticAdd, "0/0", "-Infinity", "cannot convert infinity to pg_lsn", "0A000"},
		{ArithmeticSubtract, "0/0", "-Infinity", "cannot convert infinity to pg_lsn", "0A000"},
	} {
		for _, reverse := range []bool{false, true} {
			if reverse && tc.op != ArithmeticAdd {
				continue
			}
			a, b := pgLSNUnitInput(t, types[PgLSN], tc.lsn), pgLSNUnitInput(t, types[Numeric], tc.offset)
			if reverse {
				a, b = b, a
			}
			p, err := BindArithmetic(tc.op, a.Type(), b.Type(), types[PgLSN])
			if err != nil {
				t.Fatal(err)
			}
			v, err := p.Binary(ctx, a, b, InputEnvironment{})
			if tc.code != "" {
				wrapper := ""
				if reverse {
					wrapper = "SQL function \"numeric_pl_pg_lsn\" statement 1"
				}
				pgLSNUnitError(t, err, tc.code, tc.want, wrapper)
			} else {
				if err != nil {
					t.Fatal(tc, err)
				}
				out, err := types[PgLSN].Output(ctx, v)
				if err != nil || out != tc.want {
					t.Fatal(tc, reverse, out, err)
				}
			}
			for _, pair := range [][2]Value{{Null(a.Type()), b}, {a, Null(b.Type())}} {
				v, err := p.Binary(ctx, pair[0], pair[1], InputEnvironment{})
				if err != nil || !v.IsNull() || v.Type() != types[PgLSN] {
					t.Fatal("strict NULL", v, err)
				}
			}
			canceled, cancel := context.WithCancel(ctx)
			cancel()
			if _, err := p.Binary(canceled, a, b, InputEnvironment{}); !errors.Is(err, context.Canceled) {
				t.Fatal(err)
			}
		}
	}
	for _, pair := range [][2]ID{{Numeric, PgLSN}, {PgLSN, Int8}} {
		if _, err := BindArithmetic(ArithmeticSubtract, types[pair[0]], types[pair[1]], types[PgLSN]); err == nil {
			t.Fatal("unsupported signature", pair)
		}
	}
	for _, op := range []ArithmeticOperator{ArithmeticMultiply, ArithmeticDivide, ArithmeticRemainder, ArithmeticPower} {
		if _, err := ResolveArithmetic(op, types[PgLSN], types[Numeric], false); err == nil {
			t.Fatal("unsupported operator", op)
		}
	}
	signature, err := ResolveArithmetic(ArithmeticSubtract, types[PgLSN], nil, false)
	if err != nil || signature != (ArithmeticSignature{PgLSN, PgLSN, Numeric}) {
		t.Fatal("unknown RHS inference", signature, err)
	}
}
