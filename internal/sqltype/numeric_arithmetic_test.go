package sqltype

import (
	"context"
	"errors"
	"math/big"
	"reflect"
	"testing"
)

func TestNumericArithmeticProviderOwnership(t *testing.T) {
	ctx := context.Background()
	d := testTypes()[Numeric]
	copyDescriptor := *d
	owned := &copyDescriptor
	a := numericAvgInput(t, d, "1.200")
	b := numericAvgInput(t, d, "2.30")
	originalA, originalB := a, b
	for _, op := range []ArithmeticOperator{ArithmeticAdd, ArithmeticSubtract, ArithmeticMultiply, ArithmeticDivide, ArithmeticRemainder, ArithmeticPower} {
		p, err := BindArithmetic(op, d, d, owned)
		if err != nil {
			t.Fatal(err)
		}
		result, err := p.Binary(ctx, a, b, InputEnvironment{})
		if err != nil || result.Type() != owned {
			t.Fatal(op, result, err)
		}
		held := numericOutput(result)
		for _, inputs := range [][2]Value{{Null(d), b}, {a, Null(d)}} {
			v, err := p.Binary(ctx, inputs[0], inputs[1], InputEnvironment{})
			if err != nil || !v.IsNull() || v.Type() != owned {
				t.Fatal(op, v, err)
			}
		}
		if _, err := p.Binary(ctx, Null(owned), b, InputEnvironment{}); err == nil {
			t.Fatal("wrong descriptor accepted before strict NULL")
		}
		if _, err := p.Unary(ctx, a, InputEnvironment{}); err == nil {
			t.Fatal("binary provider accepted unary call")
		}
		if _, err := p.Binary(ctx, b, a, InputEnvironment{}); err != nil {
			t.Fatal(err)
		}
		if numericOutput(result) != held || !reflect.DeepEqual(a, originalA) || !reflect.DeepEqual(b, originalB) {
			t.Fatal("mutable result/input ownership")
		}
	}
	for _, op := range []ArithmeticOperator{ArithmeticAdd, ArithmeticSubtract} {
		p, err := BindArithmetic(op, d, nil, owned)
		if err != nil {
			t.Fatal(err)
		}
		v, err := p.Unary(ctx, a, InputEnvironment{})
		if err != nil || v.Type() != owned || v.scale != 3 {
			t.Fatal(v, err)
		}
		null, err := p.Unary(ctx, Null(d), InputEnvironment{})
		if err != nil || !null.IsNull() || null.Type() != owned {
			t.Fatal(null, err)
		}
		if _, err := p.Unary(ctx, Null(owned), InputEnvironment{}); err == nil {
			t.Fatal("wrong unary descriptor accepted")
		}
		if _, err := p.Binary(ctx, a, b, InputEnvironment{}); err == nil {
			t.Fatal("unary provider accepted binary call")
		}
	}
	canceled, cancel := context.WithCancel(ctx)
	cancel()
	p, _ := BindArithmetic(ArithmeticPower, d, d, owned)
	if _, err := p.Binary(canceled, Null(d), Null(d), InputEnvironment{}); !errors.Is(err, context.Canceled) {
		t.Fatal(err)
	}
	unary, _ := BindArithmetic(ArithmeticAdd, d, nil, owned)
	if _, err := unary.Unary(canceled, Null(d), InputEnvironment{}); !errors.Is(err, context.Canceled) {
		t.Fatal(err)
	}
}

func TestNumericArithmeticScaleAndLeading(t *testing.T) {
	for _, tc := range []struct {
		digits                  string
		exponent, weight, first int64
	}{{"", 0, 0, 0}, {"1", 0, 0, 1}, {"1", -1, -1, 1000}, {"1", 4, 1, 1}, {"12345", -3, 0, 12}, {"12345", -6, -1, 123}} {
		weight, first := numericLeading(tc.digits, tc.exponent)
		if weight != tc.weight || first != tc.first {
			t.Fatal(tc, weight, first)
		}
	}
	if numericDivisionScale("1", 0, 0, "1", 0, 0) != 20 || numericDivisionScale("5", 0, 1, "2", 0, 0) != 16 || numericDivisionScale("1", -1001, 1001, "1", 0, 0) != 1000 {
		t.Fatal("division/AVG scale convention")
	}
	var w expScratch
	for _, tc := range []struct {
		a, b  string
		scale int64
		want  string
	}{{"5", "2", 0, "3"}, {"-5", "2", 0, "-3"}, {"5", "-2", 0, "-3"}, {"-5", "-2", 0, "3"}, {"4", "2", 0, "2"}} {
		var a, b, result expDecimal
		a.coefficient.SetString(tc.a, 10)
		b.coefficient.SetString(tc.b, 10)
		w.divide(&result, &a, &b, tc.scale)
		if result.coefficient.String() != tc.want || result.exponent != -tc.scale {
			t.Fatal(tc, result.coefficient.String())
		}
	}
}

func TestNumericPowerDecimalPrimitives(t *testing.T) {
	ctx := context.Background()
	for _, tc := range []struct {
		coefficient     string
		exponent, scale int64
		want            string
	}{{"9", 0, 0, "3"}, {"2499", -4, 0, "0"}, {"25", -2, 0, "1"}, {"2501", -4, 0, "1"}, {"625", 0, -1, "30"}, {"6249", -1, -1, "20"}} {
		var a, result expDecimal
		a.coefficient.SetString(tc.coefficient, 10)
		a.exponent = tc.exponent
		if err := decimalSqrt(ctx, &result, &a, tc.scale); err != nil {
			t.Fatal(err)
		}
		v, err := numericDecimalResult(ctx, testTypes()[Numeric], &result, max(int64(0), tc.scale))
		if err != nil || numericOutput(v) != tc.want {
			t.Fatal(tc, numericOutput(v), err)
		}
	}
	d := testTypes()[Numeric]
	for _, text := range []string{"0.01", "1", "2", "1e1000"} {
		var a, logarithm expDecimal
		a.setValue(numericAvgInput(t, d, text))
		if err := decimalLn(ctx, &logarithm, &a, 32); err != nil {
			t.Fatal(err)
		}
		if text == "1" && logarithm.coefficient.Sign() != 0 {
			t.Fatal("ln1 is not exact zero")
		}
		canceled, cancel := context.WithCancel(ctx)
		cancel()
		if err := decimalLn(canceled, &logarithm, &a, 32); !errors.Is(err, context.Canceled) {
			t.Fatal("ln checkpoint", err)
		}
		if err := decimalSqrt(canceled, &logarithm, &a, 32); !errors.Is(err, context.Canceled) {
			t.Fatal("sqrt checkpoint", err)
		}
		if err := decimalExp(canceled, &a, &logarithm, 32); !errors.Is(err, context.Canceled) {
			t.Fatal("exp checkpoint", err)
		}
	}
	var a, b, result expDecimal
	var w expScratch
	a.coefficient.SetInt64(123)
	a.exponent = -2
	b.coefficient.SetInt64(2)
	w.divide(&a, &a, &b, 3)
	if a.coefficient.Cmp(big.NewInt(615)) != 0 || a.exponent != -3 {
		t.Fatal("aliased decimal divide")
	}
	w.multiply(&result, &a, &a, 6)
	if result.coefficient.Cmp(big.NewInt(378225)) != 0 {
		t.Fatal("decimal multiply")
	}
}

func TestNumericPowerIntegralClassification(t *testing.T) {
	d := testTypes()[Numeric]
	for _, tc := range []struct {
		text                 string
		integral, odd, small bool
	}{{"0", true, false, true}, {"2.00", true, false, true}, {"-3", true, true, true}, {"1.01", false, false, false}, {"2147483647", true, true, true}, {"-2147483648", true, false, true}, {"2147483648", true, false, false}, {"-2147483649", true, true, false}, {"9007199254740993", true, true, false}, {"9007199254740992", true, false, false}, {"Infinity", true, false, false}, {"NaN", false, false, false}} {
		v := numericAvgInput(t, d, tc.text)
		_, small := numericInt32(v)
		if numericIntegral(v) != tc.integral || numericOdd(v) != tc.odd || small != tc.small {
			t.Fatal(tc)
		}
	}
}
