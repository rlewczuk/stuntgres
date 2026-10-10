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
	for _, id := range []ID{Int2, Int4, Int8} {
		typ := types[id]
		for _, tc := range []struct {
			op         ArithmeticOperator
			x, y, want int64
		}{{ArithmeticAdd, 7, 2, 9}, {ArithmeticSubtract, 7, 2, 5}, {ArithmeticMultiply, -7, 2, -14}, {ArithmeticDivide, -7, 2, -3}, {ArithmeticRemainder, -7, 2, -1}, {ArithmeticRemainder, 7, -2, 1}, {ArithmeticRemainder, -7, -2, -1}} {
			p, err := BindArithmetic(tc.op, typ, typ, typ)
			if err != nil {
				t.Fatal(err)
			}
			a, _ := Integer(typ, tc.x)
			b, _ := Integer(typ, tc.y)
			v, err := p.Binary(ctx, a, b, InputEnvironment{})
			n, _ := v.Integer()
			if err != nil || n != tc.want || v.Type() != typ {
				t.Fatal(tc, v, err)
			}
			v, err = p.Binary(ctx, Null(typ), b, InputEnvironment{})
			if err != nil || !v.IsNull() || v.Type() != typ {
				t.Fatal(v, err)
			}
		}
		min, max := int64(math.MinInt64), int64(math.MaxInt64)
		message := "bigint out of range"
		if id == Int4 {
			min, max = math.MinInt32, math.MaxInt32
			message = "integer out of range"
		} else if id == Int2 {
			min, max = math.MinInt16, math.MaxInt16
			message = "smallint out of range"
		}
		for _, tc := range []struct {
			op            ArithmeticOperator
			x, y          int64
			code, primary string
		}{{ArithmeticAdd, max, 1, "22003", message}, {ArithmeticSubtract, min, 1, "22003", message}, {ArithmeticMultiply, max, 2, "22003", message}, {ArithmeticMultiply, min, -1, "22003", message}, {ArithmeticDivide, min, -1, "22003", message}, {ArithmeticDivide, 7, 0, "22012", "division by zero"}, {ArithmeticRemainder, 7, 0, "22012", "division by zero"}} {
			p, _ := BindArithmetic(tc.op, typ, typ, typ)
			a, _ := Integer(typ, tc.x)
			b, _ := Integer(typ, tc.y)
			_, err := p.Binary(ctx, a, b, InputEnvironment{})
			var d *sqlstate.Error
			if !errors.As(err, &d) || d.Code != tc.code || d.Message != tc.primary || d.Position != "" || d.Detail != "" || d.Hint != "" {
				t.Fatal(tc, err)
			}
		}
		a, _ := Integer(typ, min)
		b, _ := Integer(typ, -1)
		p, _ := BindArithmetic(ArithmeticRemainder, typ, typ, typ)
		v, err := p.Binary(ctx, a, b, InputEnvironment{})
		n, _ := v.Integer()
		if err != nil || n != 0 {
			t.Fatal(v, err)
		}
		v, err = p.Binary(ctx, a, Null(typ), InputEnvironment{})
		if err != nil || !v.IsNull() || v.Type() != typ {
			t.Fatal(v, err)
		}
		p, _ = BindArithmetic(ArithmeticSubtract, typ, nil, typ)
		_, err = p.Unary(ctx, a, InputEnvironment{})
		var d *sqlstate.Error
		if !errors.As(err, &d) || d.Message != message {
			t.Fatal(err)
		}
		zero, _ := Integer(typ, 0)
		p, _ = BindArithmetic(ArithmeticDivide, typ, typ, typ)
		v, err = p.Binary(ctx, Null(typ), zero, InputEnvironment{})
		if err != nil || !v.IsNull() {
			t.Fatal(v, err)
		}
		canceled, cancel := context.WithCancel(ctx)
		cancel()
		if _, err := p.Binary(canceled, Null(typ), zero, InputEnvironment{}); !errors.Is(err, context.Canceled) {
			t.Fatal(err)
		}
	}
	typ := types[Int8]
	a, _ := Integer(typ, 9007199254740993)
	b, _ := Integer(typ, 9007199254740992)
	p, _ := BindArithmetic(ArithmeticSubtract, typ, typ, typ)
	v, err := p.Binary(ctx, a, b, InputEnvironment{})
	n, _ := v.Integer()
	if err != nil || n != 1 {
		t.Fatal(v, err)
	}
}

func TestArithmeticExactIntegerSignatures(t *testing.T) {
	types := testTypes()
	ctx := context.Background()
	for _, ids := range [][3]ID{{Int2, Int2, Int2}, {Int2, Int4, Int4}, {Int4, Int2, Int4}, {Int2, Int8, Int8}, {Int8, Int2, Int8}, {Int4, Int4, Int4}, {Int4, Int8, Int8}, {Int8, Int4, Int8}, {Int8, Int8, Int8}} {
		for _, tc := range []struct {
			op   ArithmeticOperator
			want int64
		}{{ArithmeticAdd, 9}, {ArithmeticSubtract, 5}, {ArithmeticMultiply, 14}, {ArithmeticDivide, 3}} {
			p, err := BindArithmetic(tc.op, types[ids[0]], types[ids[1]], types[ids[2]])
			if err != nil || p.ResultType() != types[ids[2]] || p.left != types[ids[0]] || p.right != types[ids[1]] {
				t.Fatal(ids, p, err)
			}
			a, _ := Integer(types[ids[0]], 7)
			b, _ := Integer(types[ids[1]], 2)
			v, err := p.Binary(ctx, a, b, InputEnvironment{})
			n, _ := v.Integer()
			if err != nil || n != tc.want || v.Type() != types[ids[2]] {
				t.Fatal(ids, tc, v, err)
			}
			v, err = p.Binary(ctx, a, Null(types[ids[1]]), InputEnvironment{})
			if err != nil || !v.IsNull() || v.Type() != types[ids[2]] {
				t.Fatal(v, err)
			}
		}
		p, err := BindArithmetic(ArithmeticRemainder, types[ids[0]], types[ids[1]], types[ids[2]])
		if ids[0] != ids[1] {
			if err == nil {
				t.Fatal("mixed modulo provider admitted", ids, p)
			}
			s, err := ResolveArithmetic(ArithmeticRemainder, types[ids[0]], types[ids[1]], false)
			if err != nil || s != (ArithmeticSignature{ids[2], ids[2], ids[2]}) {
				t.Fatal(ids, s, err)
			}
		} else if err != nil || p.ResultType() != types[ids[2]] {
			t.Fatal(ids, p, err)
		}
	}
	for _, id := range []ID{Int2, Int4, Int8} {
		for _, op := range []ArithmeticOperator{ArithmeticAdd, ArithmeticSubtract} {
			p, err := BindArithmetic(op, types[id], nil, types[id])
			if err != nil || p.ResultType() != types[id] {
				t.Fatal(p, err)
			}
			a, _ := Integer(types[id], 7)
			v, err := p.Unary(ctx, a, InputEnvironment{})
			want := int64(7)
			if op == ArithmeticSubtract {
				want = -7
			}
			n, _ := v.Integer()
			if err != nil || n != want {
				t.Fatal(v, err)
			}
			if _, err := p.Binary(ctx, a, a, InputEnvironment{}); err == nil {
				t.Fatal("unary provider accepted binary call")
			}
		}
		s, err := ResolveArithmetic(ArithmeticAdd, types[id], nil, false)
		if err != nil || s != (ArithmeticSignature{id, id, id}) {
			t.Fatal(s, err)
		}
	}
	for _, tc := range []struct {
		op          ArithmeticOperator
		left, right *Descriptor
	}{
		{ArithmeticAdd, nil, nil}, {ArithmeticAdd, types[Text], types[Text]}, {ArithmeticAdd, types[Int2], types[Bool]}, {ArithmeticAdd, types[Int2], types[Float8]},
		{ArithmeticPower, types[Int2], types[Int2]}, {ArithmeticMultiply, types[Int2], nil}, {ArithmeticOperator(255), types[Int2], types[Int2]},
	} {
		if _, err := BindArithmetic(tc.op, tc.left, tc.right, tc.left); err == nil {
			t.Fatal("invalid signature admitted", tc)
		}
	}
}

func TestArithmeticMixedIntegerBoundaries(t *testing.T) {
	types := testTypes()
	for _, tc := range []struct {
		left, right ID
		op          ArithmeticOperator
		x, y, want  int64
		message     string
	}{
		{Int2, Int4, ArithmeticDivide, -32768, -1, 32768, ""}, {Int2, Int8, ArithmeticDivide, -32768, -1, 32768, ""},
		{Int4, Int2, ArithmeticDivide, -2147483648, -1, 0, "integer out of range"}, {Int8, Int2, ArithmeticDivide, -9223372036854775808, -1, 0, "bigint out of range"},
		{Int4, Int2, ArithmeticAdd, 2147483647, 1, 0, "integer out of range"}, {Int2, Int4, ArithmeticAdd, 1, 2147483647, 0, "integer out of range"},
		{Int8, Int2, ArithmeticAdd, 9223372036854775807, 1, 0, "bigint out of range"}, {Int2, Int8, ArithmeticAdd, 1, 9223372036854775807, 0, "bigint out of range"},
		{Int4, Int2, ArithmeticMultiply, -2147483648, -1, 0, "integer out of range"}, {Int2, Int4, ArithmeticMultiply, -1, -2147483648, 0, "integer out of range"},
		{Int8, Int2, ArithmeticMultiply, -9223372036854775808, -1, 0, "bigint out of range"}, {Int2, Int8, ArithmeticMultiply, -1, -9223372036854775808, 0, "bigint out of range"},
		{Int8, Int2, ArithmeticAdd, 9007199254740993, 1, 9007199254740994, ""}, {Int8, Int2, ArithmeticMultiply, 9007199254740993, 2, 18014398509481986, ""}, {Int8, Int2, ArithmeticDivide, 9007199254740993, 3, 3002399751580331, ""},
	} {
		result := types[Int4]
		if tc.left == Int8 || tc.right == Int8 {
			result = types[Int8]
		}
		p, err := BindArithmetic(tc.op, types[tc.left], types[tc.right], result)
		if err != nil {
			t.Fatal(err)
		}
		a, _ := Integer(types[tc.left], tc.x)
		b, _ := Integer(types[tc.right], tc.y)
		v, err := p.Binary(context.Background(), a, b, InputEnvironment{})
		if tc.message != "" {
			var d *sqlstate.Error
			if !errors.As(err, &d) || d.Code != "22003" || d.Message != tc.message {
				t.Fatal(tc, err)
			}
		} else {
			n, _ := v.Integer()
			if err != nil || n != tc.want {
				t.Fatal(tc, v, err)
			}
		}
	}
}
