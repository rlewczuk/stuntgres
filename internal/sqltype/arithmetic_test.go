package sqltype

import (
	"context"
	"errors"
	"math"
	"stuntgres/internal/sqlstate"
	"testing"
)

func TestArithmeticProviders(t *testing.T) {
	types := testTypes()
	ctx := context.Background()
	for _, id := range []ID{Int4, Int8} {
		typ := types[id]
		for _, tc := range []struct {
			op         ArithmeticOperator
			x, y, want int64
		}{{ArithmeticAdd, 7, 2, 9}, {ArithmeticSubtract, 7, 2, 5}, {ArithmeticMultiply, -7, 2, -14}, {ArithmeticDivide, -7, 2, -3}, {ArithmeticRemainder, -7, 2, -1}, {ArithmeticRemainder, 7, -2, 1}, {ArithmeticRemainder, -7, -2, -1}} {
			p, err := BindArithmetic(tc.op, typ, typ)
			if err != nil {
				t.Fatal(err)
			}
			a, _ := Integer(typ, tc.x)
			b, _ := Integer(typ, tc.y)
			v, err := p.Binary(ctx, a, b)
			n, _ := v.Integer()
			if err != nil || n != tc.want || v.Type() != typ {
				t.Fatal(tc, v, err)
			}
			v, err = p.Binary(ctx, Null(typ), b)
			if err != nil || !v.IsNull() || v.Type() != typ {
				t.Fatal(v, err)
			}
		}
		min, max := int64(math.MinInt64), int64(math.MaxInt64)
		message := "bigint out of range"
		if id == Int4 {
			min, max = math.MinInt32, math.MaxInt32
			message = "integer out of range"
		}
		for _, tc := range []struct {
			op            ArithmeticOperator
			x, y          int64
			code, primary string
		}{{ArithmeticAdd, max, 1, "22003", message}, {ArithmeticSubtract, min, 1, "22003", message}, {ArithmeticMultiply, max, 2, "22003", message}, {ArithmeticMultiply, min, -1, "22003", message}, {ArithmeticDivide, min, -1, "22003", message}, {ArithmeticDivide, 7, 0, "22012", "division by zero"}, {ArithmeticRemainder, 7, 0, "22012", "division by zero"}} {
			p, _ := BindArithmetic(tc.op, typ, typ)
			a, _ := Integer(typ, tc.x)
			b, _ := Integer(typ, tc.y)
			_, err := p.Binary(ctx, a, b)
			var d *sqlstate.Error
			if !errors.As(err, &d) || d.Code != tc.code || d.Message != tc.primary || d.Position != "" || d.Detail != "" || d.Hint != "" {
				t.Fatal(tc, err)
			}
		}
		a, _ := Integer(typ, min)
		b, _ := Integer(typ, -1)
		p, _ := BindArithmetic(ArithmeticRemainder, typ, typ)
		v, err := p.Binary(ctx, a, b)
		n, _ := v.Integer()
		if err != nil || n != 0 {
			t.Fatal(v, err)
		}
		v, err = p.Binary(ctx, a, Null(typ))
		if err != nil || !v.IsNull() || v.Type() != typ {
			t.Fatal(v, err)
		}
		p, _ = BindArithmetic(ArithmeticSubtract, typ, nil)
		_, err = p.Unary(ctx, a)
		var d *sqlstate.Error
		if !errors.As(err, &d) || d.Message != message {
			t.Fatal(err)
		}
		zero, _ := Integer(typ, 0)
		p, _ = BindArithmetic(ArithmeticDivide, typ, typ)
		v, err = p.Binary(ctx, Null(typ), zero)
		if err != nil || !v.IsNull() {
			t.Fatal(v, err)
		}
		canceled, cancel := context.WithCancel(ctx)
		cancel()
		if _, err := p.Binary(canceled, Null(typ), zero); !errors.Is(err, context.Canceled) {
			t.Fatal(err)
		}
	}
	typ := types[Int8]
	a, _ := Integer(typ, 9007199254740993)
	b, _ := Integer(typ, 9007199254740992)
	p, _ := BindArithmetic(ArithmeticSubtract, typ, typ)
	v, err := p.Binary(ctx, a, b)
	n, _ := v.Integer()
	if err != nil || n != 1 {
		t.Fatal(v, err)
	}
}
