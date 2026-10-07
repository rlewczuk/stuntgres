package sqltype

import (
	"context"
	"errors"
	"testing"
)

func TestBitwiseProviders(t *testing.T) {
	types := testTypes()
	ctx := context.Background()
	for _, id := range []ID{Int2, Int4, Int8} {
		typ := types[id]
		for _, tc := range []struct {
			op         BitwiseOperator
			x, y, want int64
		}{
			{BitwiseAnd, 91, 15, 11}, {BitwiseOr, 32, 3, 35}, {BitwiseXor, 17, 5, 20}, {BitwiseAnd, -1, 15, 15}, {BitwiseShiftLeft, 1, 4, 16}, {BitwiseShiftRight, -8, 2, -2},
		} {
			rt := typ
			if tc.op >= BitwiseShiftLeft {
				rt = types[Int4]
			}
			p, err := BindBitwise(tc.op, typ, rt)
			if err != nil {
				t.Fatal(err)
			}
			a, _ := Integer(typ, tc.x)
			b, _ := Integer(rt, tc.y)
			v, err := p.Binary(ctx, a, b)
			n, _ := v.Integer()
			if err != nil || n != tc.want || v.Type() != typ {
				t.Fatal(id, tc, v, err)
			}
			for _, pair := range [][2]Value{{Null(typ), b}, {a, Null(rt)}} {
				v, err = p.Binary(ctx, pair[0], pair[1])
				if err != nil || !v.IsNull() || v.Type() != typ {
					t.Fatal(v, err)
				}
			}
			canceled, cancel := context.WithCancel(ctx)
			cancel()
			if _, err = p.Binary(canceled, Null(typ), b); !errors.Is(err, context.Canceled) {
				t.Fatal(err)
			}
			foreign, _ := Integer(testTypes()[id], 1)
			if _, err = p.Binary(ctx, foreign, b); err == nil {
				t.Fatal("foreign value accepted")
			}
			foreignRight, _ := Integer(testTypes()[rt.id], tc.y)
			if _, err = p.Binary(ctx, a, foreignRight); err == nil {
				t.Fatal("foreign right value accepted")
			}
		}
		p, err := BindBitwise(BitwiseNot, typ, nil)
		if err != nil {
			t.Fatal(err)
		}
		for _, tc := range []struct{ x, want int64 }{{0, -1}, {-1, 0}, {1, -2}} {
			a, _ := Integer(typ, tc.x)
			v, err := p.Unary(ctx, a)
			n, _ := v.Integer()
			if err != nil || n != tc.want {
				t.Fatal(v, err)
			}
		}
		v, err := p.Unary(ctx, Null(typ))
		if err != nil || !v.IsNull() {
			t.Fatal(v, err)
		}
		canceled, cancel := context.WithCancel(ctx)
		cancel()
		if _, err = p.Unary(canceled, Null(typ)); !errors.Is(err, context.Canceled) {
			t.Fatal(err)
		}
		foreign, _ := Integer(testTypes()[id], 1)
		if _, err = p.Unary(ctx, foreign); err == nil {
			t.Fatal("foreign unary value accepted")
		}
	}
	for _, tc := range []struct {
		op          BitwiseOperator
		left, right *Descriptor
	}{
		{BitwiseAnd, nil, nil}, {BitwiseNot, types[Int4], types[Int4]}, {BitwiseAnd, types[Int2], types[Int4]}, {BitwiseShiftLeft, types[Int8], types[Int8]}, {BitwiseOperator(255), types[Int4], types[Int4]},
	} {
		if _, err := BindBitwise(tc.op, tc.left, tc.right); err == nil {
			t.Fatal("invalid signature accepted", tc)
		}
	}
	if _, err := (Bitwise{}).Unary(ctx, Null(types[Int4])); err == nil {
		t.Fatal("zero provider accepted")
	}
	if _, err := (Bitwise{}).Binary(ctx, Null(types[Int4]), Null(types[Int4])); err == nil {
		t.Fatal("zero provider accepted")
	}
}
