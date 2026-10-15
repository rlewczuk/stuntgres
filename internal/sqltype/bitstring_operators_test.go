package sqltype

import (
	"context"
	"errors"
	"math"
	"strings"
	"stuntgres/internal/sqlstate"
	"testing"
	"unsafe"
)

func bitOperatorValue(t *testing.T, d *Descriptor, text string) Value {
	t.Helper()
	v, err := BitString(d, text)
	if err != nil {
		t.Fatal(err)
	}
	return v
}

func TestBitStringOperatorsValues(t *testing.T) {
	types := testTypes()
	bit, integer := types[Bit], types[Int4]
	ctx := context.Background()
	for _, tc := range []struct {
		op         BitwiseOperator
		a, b, want string
	}{
		{BitwiseAnd, "10001", "01101", "00001"}, {BitwiseOr, "10001", "01101", "11101"}, {BitwiseXor, "10001", "01101", "11100"},
		{BitwiseAnd, "", "", ""}, {BitwiseOr, "", "", ""}, {BitwiseXor, "", "", ""},
		{BitwiseAnd, "101010101", "010101010", "000000000"}, {BitwiseOr, "101010101", "010101010", "111111111"}, {BitwiseXor, "101010101", "010101010", "111111111"},
		{BitwiseAnd, "1" + strings.Repeat("0", 63) + "1", "0" + strings.Repeat("1", 63) + "0", strings.Repeat("0", 65)},
		{BitwiseOr, "1" + strings.Repeat("0", 63) + "1", "0" + strings.Repeat("1", 63) + "0", strings.Repeat("1", 65)},
		{BitwiseXor, "1" + strings.Repeat("0", 63) + "1", "0" + strings.Repeat("1", 63) + "0", strings.Repeat("1", 65)},
	} {
		p, err := BindBitStringBitwise(tc.op, bit, bit)
		if err != nil {
			t.Fatal(err)
		}
		a, b := bitOperatorValue(t, bit, tc.a), bitOperatorValue(t, bit, tc.b)
		got, err := p.Binary(ctx, a, b)
		if err != nil || got.typ != bit || got.null || got.text != tc.want {
			t.Fatal(tc, got, err)
		}
		for _, pair := range [][2]Value{{Null(bit), b}, {a, Null(bit)}} {
			got, err := p.Binary(ctx, pair[0], pair[1])
			if err != nil || !got.null || got.typ != bit {
				t.Fatal(got, err)
			}
		}
		if a.text != tc.a || b.text != tc.b {
			t.Fatal("mutated inputs")
		}
	}
	p, err := BindBitStringBitwise(BitwiseNot, bit, nil)
	if err != nil {
		t.Fatal(err)
	}
	for _, tc := range [][2]string{{"10001", "01110"}, {"", ""}, {strings.Repeat("0", 9), strings.Repeat("1", 9)}, {strings.Repeat("0", 65), strings.Repeat("1", 65)}} {
		got, err := p.Unary(ctx, bitOperatorValue(t, bit, tc[0]))
		if err != nil || got.typ != bit || got.null || got.text != tc[1] {
			t.Fatal(got, err)
		}
	}
	got, err := p.Unary(ctx, Null(bit))
	if err != nil || !got.null || got.typ != bit {
		t.Fatal(got, err)
	}
	for _, tc := range []struct {
		n           int64
		left, right string
	}{
		{0, "10001", "10001"}, {1, "00010", "01000"}, {3, "01000", "00010"}, {4, "10000", "00001"},
		{5, "00000", "00000"}, {6, "00000", "00000"}, {32, "00000", "00000"}, {64, "00000", "00000"},
		{-2, "00100", "00100"}, {-3, "00010", "01000"}, {-5, "00000", "00000"}, {-6, "00000", "00000"},
		{math.MinInt32, "00000", "00000"}, {math.MaxInt32, "00000", "00000"},
	} {
		for _, op := range []BitwiseOperator{BitwiseShiftLeft, BitwiseShiftRight} {
			p, err := BindBitStringBitwise(op, bit, integer)
			if err != nil {
				t.Fatal(err)
			}
			count, err := Integer(integer, tc.n)
			if err != nil {
				t.Fatal(err)
			}
			want := tc.left
			if op == BitwiseShiftRight {
				want = tc.right
			}
			for _, text := range []string{"10001", ""} {
				v := bitOperatorValue(t, bit, text)
				got, err := p.Binary(ctx, v, count)
				w := want
				if text == "" {
					w = ""
				}
				if err != nil || got.typ != bit || got.null || got.text != w {
					t.Fatal(op, tc.n, got, err)
				}
				if tc.n == 0 && unsafe.StringData(got.text) != unsafe.StringData(v.text) {
					t.Fatal("shift zero copied payload")
				}
			}
			for _, pair := range [][2]Value{{Null(bit), count}, {bitOperatorValue(t, bit, "1"), Null(integer)}} {
				got, err := p.Binary(ctx, pair[0], pair[1])
				if err != nil || !got.null || got.typ != bit {
					t.Fatal(got, err)
				}
			}
		}
	}
}

func TestBitStringOperatorsValidation(t *testing.T) {
	types, foreign := testTypes(), testTypes()
	bit, integer := types[Bit], types[Int4]
	ctx := context.Background()
	for _, tc := range []struct {
		op          BitwiseOperator
		left, right *Descriptor
	}{
		{BitwiseAnd, nil, nil}, {BitwiseAnd, bit, foreign[Bit]}, {BitwiseAnd, bit, types[VarBit]},
		{BitwiseNot, bit, bit}, {BitwiseShiftLeft, bit, types[Int8]}, {BitwiseShiftRight, types[VarBit], integer},
		{BitwiseOperator(255), bit, bit}, {BitwiseAnd, integer, integer},
	} {
		if _, err := BindBitStringBitwise(tc.op, tc.left, tc.right); err == nil {
			t.Fatal("invalid signature", tc)
		}
	}
	if _, err := BindBitwise(BitwiseAnd, bit, bit); err == nil {
		t.Fatal("integral provider admitted bits")
	}
	for _, op := range []BitwiseOperator{BitwiseAnd, BitwiseOr, BitwiseXor} {
		p, err := BindBitStringBitwise(op, bit, bit)
		if err != nil {
			t.Fatal(err)
		}
		for _, pair := range [][2]string{{"1", "00"}, {"00", "1"}, {"", "0"}, {"0", ""}} {
			_, err := p.Binary(ctx, bitOperatorValue(t, bit, pair[0]), bitOperatorValue(t, bit, pair[1]))
			var d *sqlstate.Error
			want := []string{"cannot AND bit strings of different sizes", "cannot OR bit strings of different sizes", "cannot XOR bit strings of different sizes"}[op]
			if !errors.As(err, &d) || *d != *(failure("22026", want).(*sqlstate.Error)) {
				t.Fatal(err)
			}
		}
		if _, err := p.Binary(ctx, Null(bit), Null(foreign[Bit])); err == nil {
			t.Fatal("foreign right NULL")
		}
		if _, err := p.Binary(ctx, Null(foreign[Bit]), Null(bit)); err == nil {
			t.Fatal("foreign left NULL")
		}
		if _, err := p.Unary(ctx, Null(bit)); err == nil {
			t.Fatal("binary provider used as unary")
		}
	}
	p, err := BindBitStringBitwise(BitwiseNot, bit, nil)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := p.Unary(ctx, Null(foreign[Bit])); err == nil {
		t.Fatal("foreign unary NULL")
	}
	if _, err := p.Binary(ctx, Null(bit), Null(bit)); err == nil {
		t.Fatal("unary provider used as binary")
	}
	if _, err := (BitStringBitwise{}).Unary(ctx, Null(bit)); err == nil {
		t.Fatal("zero unary provider")
	}
	if _, err := (BitStringBitwise{}).Binary(ctx, Null(bit), Null(bit)); err == nil {
		t.Fatal("zero binary provider")
	}
}

func TestBitStringOperatorsConcat(t *testing.T) {
	types, foreign := testTypes(), testTypes()
	v := types[VarBit]
	p, err := BindBinary("||", v.Usage(), v.Usage(), v.Usage())
	if err != nil {
		t.Fatal(err)
	}
	for _, tc := range [][3]string{{"10001", "011", "10001011"}, {"", "00100", "00100"}, {"00100", "", "00100"}, {"", "", ""}} {
		a, b := bitOperatorValue(t, v, tc[0]), bitOperatorValue(t, v, tc[1])
		got, err := p.Evaluate(context.Background(), a, b)
		if err != nil || got.typ != v || got.null || got.text != tc[2] {
			t.Fatal(got, err)
		}
		if a.text == "" && unsafe.StringData(got.text) != unsafe.StringData(b.text) || b.text == "" && unsafe.StringData(got.text) != unsafe.StringData(a.text) {
			t.Fatal("empty concat copied payload")
		}
	}
	for _, pair := range [][2]Value{{Null(v), bitOperatorValue(t, v, "1")}, {bitOperatorValue(t, v, "1"), Null(v)}} {
		got, err := p.Evaluate(context.Background(), pair[0], pair[1])
		if err != nil || !got.null || got.typ != v {
			t.Fatal(got, err)
		}
	}
	bad := v.Usage()
	bad.Typmod = 3
	for _, args := range [][]Usage{{v.Usage()}, {v.Usage(), v.Usage(), v.Usage()}, {v.Usage(), foreign[VarBit].Usage()}, {v.Usage(), bad}, {types[Bit].Usage(), types[Bit].Usage()}} {
		if _, err := BindBinary("||", v.Usage(), args...); err == nil {
			t.Fatal("invalid concat signature", args)
		}
	}
	for _, result := range []Usage{bad, foreign[VarBit].Usage(), types[Bit].Usage()} {
		if _, err := BindBinary("||", result, v.Usage(), v.Usage()); err == nil {
			t.Fatal("invalid result", result)
		}
	}
	if _, err := p.Evaluate(context.Background(), Null(v), Null(foreign[VarBit])); err == nil {
		t.Fatal("foreign concat NULL")
	}
	if n, err := bitStringConcatSize(math.MaxInt32-8, 1); err != nil || n != math.MaxInt32-7 {
		t.Fatal(n, err)
	}
	_, err = bitStringConcatSize(math.MaxInt32-7, 1)
	var d *sqlstate.Error
	if !errors.As(err, &d) || d.Code != "54000" || d.Message != "bit string length exceeds the maximum allowed (2147483640)" {
		t.Fatal(err)
	}
}

func TestBitStringOperatorsComparisons(t *testing.T) {
	types := testTypes()
	for _, id := range []ID{Bit, VarBit} {
		d := types[id]
		for _, tc := range []struct {
			a, b string
			sign int
		}{{"", "0", -1}, {"0", "00", -1}, {"00", "001", -1}, {"001", "01", -1}, {"01", "1", -1}, {"1", "10", -1}, {"1", "01", 1}, {"10", "100", -1}, {"10", "10", 0}} {
			for _, op := range []ComparisonOperator{CompareEqual, CompareNotEqual, CompareLess, CompareLessEqual, CompareGreater, CompareGreaterEqual} {
				p, err := BindComparisonPredicate(op, d, d, types[Bool], CollationRef{}, false)
				if err != nil {
					t.Fatal(err)
				}
				a, b := bitOperatorValue(t, d, tc.a), bitOperatorValue(t, d, tc.b)
				got, err := p.Evaluate(context.Background(), a, b, InputEnvironment{})
				want := []bool{tc.sign == 0, tc.sign != 0, tc.sign < 0, tc.sign <= 0, tc.sign > 0, tc.sign >= 0}[op]
				if err != nil || got.typ != types[Bool] || got.null || got.boolean != want {
					t.Fatal(id, tc, op, got, err)
				}
				for _, pair := range [][2]Value{{Null(d), b}, {a, Null(d)}} {
					got, err := p.Evaluate(context.Background(), pair[0], pair[1], InputEnvironment{})
					if err != nil || !got.null || got.typ != types[Bool] {
						t.Fatal(got, err)
					}
				}
			}
		}
		if _, err := BindOrdering(d.Usage()); err == nil {
			t.Fatal("bit ordering widened")
		}
		if _, err := BindGrouping(d.Usage()); err == nil {
			t.Fatal("bit grouping widened")
		}
	}
	coercion, err := PlanCoercion(types[Bit].Usage(), types[VarBit].Usage(), Implicit)
	if err != nil {
		t.Fatal(err)
	}
	a, err := coercion.Apply(context.Background(), bitOperatorValue(t, types[Bit], "1"), InputEnvironment{}, OutputEnvironment{})
	if err != nil {
		t.Fatal(err)
	}
	p, err := BindComparison(types[VarBit], types[VarBit], CollationRef{}, false)
	if err != nil {
		t.Fatal(err)
	}
	if sign, err := p.Compare(context.Background(), a, bitOperatorValue(t, types[VarBit], "01"), InputEnvironment{}); err != nil || sign != 1 {
		t.Fatal(sign, err)
	}
}

func TestBitStringOperatorsCancellation(t *testing.T) {
	types := testTypes()
	bit := types[Bit]
	a := bitOperatorValue(t, bit, strings.Repeat("1", 65536))
	b := bitOperatorValue(t, bit, strings.Repeat("0", 65536))
	for _, op := range []BitwiseOperator{BitwiseNot, BitwiseAnd, BitwiseOr, BitwiseXor, BitwiseShiftLeft, BitwiseShiftRight} {
		rt := bit
		if op == BitwiseNot {
			rt = nil
		}
		if op >= BitwiseShiftLeft {
			rt = types[Int4]
		}
		p, err := BindBitStringBitwise(op, bit, rt)
		if err != nil {
			t.Fatal(err)
		}
		// Initial validation and first loop checkpoint pass; cancellation happens
		// after real output has already been written, not at admission.
		ctx := &mathCheckpointContext{Context: context.Background(), remaining: 4}
		var got Value
		if op == BitwiseNot {
			got, err = p.Unary(ctx, a)
		} else {
			right := b
			if op >= BitwiseShiftLeft {
				right, _ = Integer(rt, 32768)
			}
			got, err = p.Binary(ctx, a, right)
		}
		if !errors.Is(err, context.Canceled) || got != (Value{}) {
			t.Fatal(op, got, err)
		}
	}
	v := types[VarBit]
	p, err := BindBinary("||", v.Usage(), v.Usage(), v.Usage())
	if err != nil {
		t.Fatal(err)
	}
	got, err := p.Evaluate(&mathCheckpointContext{Context: context.Background(), remaining: 4}, bitOperatorValue(t, v, strings.Repeat("1", 65536)), bitOperatorValue(t, v, "0"))
	if !errors.Is(err, context.Canceled) || got != (Value{}) {
		t.Fatal(got, err)
	}
}
