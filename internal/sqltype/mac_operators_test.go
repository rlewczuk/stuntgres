package sqltype

import (
	"context"
	"errors"
	"testing"
)

func macOperatorValue(t *testing.T, d *Descriptor, bits uint64) Value {
	t.Helper()
	var v Value
	var err error
	if d.ID() == Macaddr {
		v, err = MACADDRValue(d, bits)
	} else {
		v, err = MACADDR8Value(d, bits)
	}
	if err != nil {
		t.Fatal(err)
	}
	return v
}

func TestMACBitwiseProvider(t *testing.T) {
	types, foreign := testTypes(), testTypes()
	ctx := context.Background()
	canceled, cancel := context.WithCancel(ctx)
	cancel()
	for _, width := range []struct {
		id                 ID
		a, b, not, and, or uint64
	}{
		{Macaddr, 0x1234567890ab, 0x0ff0aa55ff00, 0xedcba9876f54, 0x023002509000, 0x1ff4fe7dffab},
		{Macaddr8, 0x9234567890abcdef, 0x0ff0aa55ff0033cc, 0x6dcba9876f543210, 0x02300250900001cc, 0x9ff4fe7dffabffef},
	} {
		d := types[width.id]
		a, b := macOperatorValue(t, d, width.a), macOperatorValue(t, d, width.b)
		for _, tc := range []struct {
			op   BitwiseOperator
			want uint64
		}{{BitwiseNot, width.not}, {BitwiseAnd, width.and}, {BitwiseOr, width.or}} {
			right := d
			if tc.op == BitwiseNot {
				right = nil
			}
			p, err := BindMACBitwise(tc.op, d, right)
			if err != nil {
				t.Fatal(err)
			}
			evaluate := func(c context.Context, x, y Value) (Value, error) {
				if right == nil {
					return p.Unary(c, x)
				}
				return p.Binary(c, x, y)
			}
			v, err := evaluate(ctx, a, b)
			if err != nil || v.typ != d || uint64(v.integer) != tc.want {
				t.Fatal(width.id, tc.op, v, err)
			}
			for _, pair := range [][2]Value{{Null(d), b}, {a, Null(d)}, {Null(d), Null(d)}} {
				if right == nil && !pair[0].IsNull() {
					continue
				}
				v, err := evaluate(ctx, pair[0], pair[1])
				if err != nil || !v.IsNull() || v.typ != d {
					t.Fatal("typed strict NULL", v, err)
				}
			}
			for _, x := range []Value{a, Null(d)} {
				if _, err := evaluate(canceled, x, b); !errors.Is(err, context.Canceled) {
					t.Fatal("cancellation", err)
				}
			}
			wrong := macOperatorValue(t, foreign[width.id], width.a)
			if _, err := evaluate(ctx, wrong, b); err == nil {
				t.Fatal("foreign left descriptor admitted")
			}
			if right != nil {
				if _, err := evaluate(ctx, Null(d), wrong); err == nil {
					t.Fatal("NULL hid foreign right descriptor")
				}
			}
			if width.id == Macaddr {
				for _, payload := range []int64{-1, 1 << 48} {
					bad := a
					bad.integer = payload
					if _, err := evaluate(ctx, bad, b); err == nil {
						t.Fatal("corrupt MACADDR payload admitted")
					}
					if right != nil {
						if _, err := evaluate(ctx, Null(d), bad); err == nil {
							t.Fatal("strict NULL hid corrupt payload")
						}
					}
				}
			}
			var result Value
			allocs := testing.AllocsPerRun(100, func() { result, err = evaluate(ctx, a, b) })
			if allocs != 0 || err != nil || uint64(result.integer) != tc.want {
				t.Fatal("successful-path allocations", allocs, result, err)
			}
		}
	}
	for _, tc := range []struct {
		op          BitwiseOperator
		left, right *Descriptor
	}{
		{BitwiseAnd, nil, nil}, {BitwiseNot, types[Int8], nil},
		{BitwiseNot, types[Macaddr], types[Macaddr]}, {BitwiseAnd, types[Macaddr], nil},
		{BitwiseAnd, types[Macaddr], types[Macaddr8]}, {BitwiseOr, types[Macaddr8], foreign[Macaddr8]},
		{BitwiseXor, types[Macaddr], types[Macaddr]}, {BitwiseShiftLeft, types[Macaddr8], types[Int4]},
		{BitwiseShiftRight, types[Macaddr], types[Int4]}, {BitwiseOperator(255), types[Macaddr], nil},
	} {
		if _, err := BindMACBitwise(tc.op, tc.left, tc.right); err == nil {
			t.Fatal("invalid bitwise signature admitted", tc)
		}
	}
	if _, err := (MACBitwise{}).Unary(ctx, Null(types[Macaddr])); err == nil {
		t.Fatal("zero unary provider admitted")
	}
	if _, err := (MACBitwise{}).Binary(ctx, Null(types[Macaddr]), Null(types[Macaddr])); err == nil {
		t.Fatal("zero binary provider admitted")
	}
}

func TestMACUnaryProvider(t *testing.T) {
	types, foreign := testTypes(), testTypes()
	ctx := context.Background()
	canceled, cancel := context.WithCancel(ctx)
	cancel()
	for _, tc := range []struct {
		kind          MACUnaryKind
		input, result ID
		bits, want    uint64
	}{
		{MACTrunc, Macaddr, Macaddr, 0x1234567890ab, 0x123456000000},
		{MACTrunc, Macaddr8, Macaddr8, 0x9234567890abcdef, 0x9234560000000000},
		{MACSet7Bit, Macaddr8, Macaddr8, 0x8034567890abcdef, 0x8234567890abcdef},
		{MACSet7Bit, Macaddr8, Macaddr8, 0x0234567890abcdef, 0x0234567890abcdef},
		{MACSet7Bit, Macaddr8, Macaddr8, 0x0134567890abcdef, 0x0334567890abcdef},
		{MACSet7Bit, Macaddr, Macaddr8, 0x003456abcdef, 0x023456fffeabcdef},
		{MACSet7Bit, Macaddr, Macaddr8, 0, 0x020000fffe000000},
	} {
		d, result := types[tc.input], types[tc.result]
		p, err := BindMACUnary(tc.kind, d, result)
		if err != nil {
			t.Fatal(err)
		}
		a := macOperatorValue(t, d, tc.bits)
		v, err := p.Evaluate(ctx, a)
		if err != nil || v.typ != result || uint64(v.integer) != tc.want {
			t.Fatal(tc, v, err)
		}
		again, err := BindMACUnary(tc.kind, result, result)
		if err != nil {
			t.Fatal(err)
		}
		if next, err := again.Evaluate(ctx, v); err != nil || next != v {
			t.Fatal("idempotence", next, v, err)
		}
		v, err = p.Evaluate(ctx, Null(d))
		if err != nil || !v.IsNull() || v.typ != result {
			t.Fatal("typed NULL", v, err)
		}
		for _, x := range []Value{a, Null(d)} {
			if _, err := p.Evaluate(canceled, x); !errors.Is(err, context.Canceled) {
				t.Fatal(err)
			}
		}
		if _, err := p.Evaluate(ctx, macOperatorValue(t, foreign[tc.input], tc.bits)); err == nil {
			t.Fatal("foreign input admitted")
		}
		if tc.input == Macaddr {
			for _, payload := range []int64{-1, 1 << 48} {
				bad := a
				bad.integer = payload
				if _, err := p.Evaluate(ctx, bad); err == nil {
					t.Fatal("corrupt input admitted")
				}
			}
		}
		allocs := testing.AllocsPerRun(100, func() { v, err = p.Evaluate(ctx, a) })
		if allocs != 0 || err != nil || uint64(v.integer) != tc.want {
			t.Fatal("successful-path allocations", allocs, v, err)
		}
	}
	for _, tc := range []struct {
		kind          MACUnaryKind
		input, result *Descriptor
	}{
		{MACTrunc, nil, nil}, {MACTrunc, types[Int8], types[Int8]},
		{MACTrunc, types[Macaddr], types[Macaddr8]}, {MACTrunc, types[Macaddr], foreign[Macaddr]},
		{MACSet7Bit, types[Macaddr8], types[Macaddr]}, {MACSet7Bit, types[Macaddr8], foreign[Macaddr8]},
		{MACUnaryKind(255), types[Macaddr], types[Macaddr]},
	} {
		if _, err := BindMACUnary(tc.kind, tc.input, tc.result); err == nil {
			t.Fatal("invalid unary signature admitted", tc)
		}
	}
	if _, err := (MACUnary{}).Evaluate(ctx, Null(types[Macaddr])); err == nil {
		t.Fatal("zero unary provider admitted")
	}
	for _, pair := range [][2]*Descriptor{{types[Macaddr], types[Macaddr8]}, {types[Macaddr8], types[Macaddr]}} {
		if _, err := PlanCoercion(pair[0].Usage(), pair[1].Usage(), Implicit); err == nil {
			t.Fatal("function-local expansion enabled a general cast")
		}
	}
}
