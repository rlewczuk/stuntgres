package sqltype

import (
	"context"
	"errors"
	"math"
	"reflect"
	"testing"

	"stuntgres/internal/sqlstate"
)

func moneyUnitFailure(t *testing.T, got Value, err error, code, message string) {
	t.Helper()
	var diagnostic *sqlstate.Error
	want := &sqlstate.Error{Severity: "ERROR", SeverityUnlocalized: "ERROR", Code: code, Message: message}
	if got != (Value{}) || !errors.As(err, &diagnostic) || !reflect.DeepEqual(diagnostic, want) {
		t.Fatal("money failure", got, err, want)
	}
}

// REL_18_0 cash_div_int64 has an unguarded minimum/-1 division. These
// actual provider units assert the approved safe native exception, not oracle
// parity. Never move this witness into the shared PostgreSQL SQL fixture.
func TestMoneyMinimumDivisionOverflow(t *testing.T) {
	types := testTypes()
	ctx := context.Background()
	for _, id := range []ID{Int2, Int4, Int8} {
		p, err := BindArithmetic(ArithmeticDivide, types[Money], types[id], types[Money])
		if err != nil {
			t.Fatal(err)
		}
		a, err := MoneyValue(types[Money], math.MinInt64)
		if err != nil {
			t.Fatal(err)
		}
		b, err := Integer(types[id], -1)
		if err != nil {
			t.Fatal(err)
		}
		beforeA, beforeB := a, b
		v, err := p.Binary(ctx, a, b, InputEnvironment{})
		moneyUnitFailure(t, v, err, "22003", "money out of range")
		if !SameDatum(a, beforeA) || !SameDatum(b, beforeB) {
			t.Fatal("operands mutated")
		}
		b, _ = Integer(types[id], 2)
		v, err = p.Binary(ctx, a, b, InputEnvironment{})
		if err != nil || v.typ != types[Money] || v.integer != -4611686018427387904 {
			t.Fatal("division after error", v, err)
		}
	}
}

func TestMoneyCodecAndExactCasts(t *testing.T) {
	types := testTypes()
	ctx := context.Background()
	for _, localeName := range []string{"C", "POSIX", ""} {
		locale, ok := ParseCLocale(localeName)
		if !ok {
			t.Fatal(localeName)
		}
		for _, tc := range []struct {
			input, output, numeric string
			units                  int64
		}{
			{"92233720368547758.07", "$92,233,720,368,547,758.07", "92233720368547758.07", math.MaxInt64},
			{"-92233720368547758.08", "-$92,233,720,368,547,758.08", "-92233720368547758.08", math.MinInt64},
			{"$  -  $ 1.235", "-$1.24", "-1.24", -124},
			{"1,2,3.4", "$123.40", "123.40", 12340},
			{"", "$0.00", "0.00", 0},
		} {
			v, err := types[Money].InputWithEnvironment(ctx, tc.input, -1, InputEnvironment{MonetaryLocale: locale})
			if err != nil || v.integer != tc.units {
				t.Fatal(tc, v, err)
			}
			units, err := v.MoneyUnits()
			if err != nil || units != tc.units {
				t.Fatal(units, err)
			}
			text, err := types[Money].OutputWithEnvironment(ctx, v, OutputEnvironment{MonetaryLocale: locale})
			if err != nil || text != tc.output {
				t.Fatal(tc, text, err)
			}
			p, err := PlanCoercion(types[Money].Usage(), types[Numeric].Usage(), Explicit)
			if err != nil || !p.SessionDependent() {
				t.Fatal(p, err)
			}
			n, err := p.Apply(ctx, v, InputEnvironment{MonetaryLocale: locale}, OutputEnvironment{})
			if err != nil || numericOutput(n) != tc.numeric {
				t.Fatal(tc, n, err)
			}
			back, err := PlanCoercion(types[Numeric].Usage(), types[Money].Usage(), Assignment)
			if err != nil || !back.SessionDependent() {
				t.Fatal(back, err)
			}
			restored, err := back.Apply(ctx, n, InputEnvironment{MonetaryLocale: locale}, OutputEnvironment{})
			if err != nil || !SameDatum(v, restored) {
				t.Fatal(v, restored, err)
			}
		}
	}
	for _, input := range []string{"92233720368547758.075", "-92233720368547758.085"} {
		v, err := types[Money].Input(ctx, input, -1)
		moneyUnitFailure(t, v, err, "22003", "value \""+input+"\" is out of range for type money")
	}
	for _, input := range []string{"1.234,5", "oops"} {
		v, err := types[Money].Input(ctx, input, -1)
		moneyUnitFailure(t, v, err, "22P02", "invalid input syntax for type money: \""+input+"\"")
	}
	if types[791].Concrete() {
		t.Fatal("money array accidentally promoted")
	}
	if _, err := Integer(types[Money], 1); err == nil {
		t.Fatal("integer constructor admitted money")
	}
	if _, err := MoneyValue(types[Int8], 1); err == nil {
		t.Fatal("money constructor admitted integer")
	}
	if !types[Money].OutputSessionDependent() {
		t.Fatal("money output volatility")
	}
	literal, err := types[Money].SQLLiteral(ctx, Null(types[Money]))
	if err != nil || literal != "CAST(NULL AS money)" {
		t.Fatal(literal, err)
	}
}

func TestMoneyArithmeticResolutionAndBoundaries(t *testing.T) {
	types := testTypes()
	ctx := context.Background()
	for _, id := range []ID{Int2, Int4, Int8, Float4, Float8} {
		for _, op := range []ArithmeticOperator{ArithmeticMultiply, ArithmeticDivide} {
			p, err := BindArithmetic(op, types[Money], types[id], types[Money])
			if err != nil {
				t.Fatal(err)
			}
			a, _ := MoneyValue(types[Money], 87808)
			b, _ := Integer(types[id], 11)
			if isFloat(id) {
				b, _ = Float(types[id], 11)
			}
			v, err := p.Binary(ctx, a, b, InputEnvironment{})
			want := int64(965888)
			if op == ArithmeticDivide {
				want = 7982
				if isFloat(id) {
					want = 7983
				}
			}
			if err != nil || v.integer != want || v.typ != types[Money] {
				t.Fatal(id, op, v, err)
			}
			v, err = p.Binary(ctx, Null(types[Money]), b, InputEnvironment{})
			if err != nil || !v.null || v.typ != types[Money] {
				t.Fatal(v, err)
			}
		}
	}
	for _, units := range []int64{1, 5} {
		p, _ := BindArithmetic(ArithmeticDivide, types[Money], types[Float8], types[Money])
		a, _ := MoneyValue(types[Money], units)
		b, _ := Float(types[Float8], 2)
		v, err := p.Binary(ctx, a, b, InputEnvironment{})
		if err != nil || v.integer != units/2 {
			t.Fatal("nearest even", units, v, err)
		}
	}
	for _, op := range []ArithmeticOperator{ArithmeticAdd, ArithmeticSubtract, ArithmeticMultiply} {
		right := types[Money]
		a, _ := MoneyValue(types[Money], math.MaxInt64)
		b, _ := MoneyValue(types[Money], 1)
		if op == ArithmeticSubtract {
			b, _ = MoneyValue(types[Money], -1)
		}
		if op == ArithmeticMultiply {
			right = types[Int8]
			b, _ = Integer(right, 2)
		}
		p, err := BindArithmetic(op, types[Money], right, types[Money])
		if err != nil {
			t.Fatal(err)
		}
		v, err := p.Binary(ctx, a, b, InputEnvironment{})
		moneyUnitFailure(t, v, err, "22003", "money out of range")
	}
	for _, tc := range []struct {
		right *Descriptor
		want  ArithmeticSignature
	}{
		{nil, ArithmeticSignature{Money, Money, Float8}},
		{types[Int4], ArithmeticSignature{Money, Int4, Money}},
		{types[Numeric], ArithmeticSignature{Money, Float8, Money}},
	} {
		got, err := ResolveArithmetic(ArithmeticDivide, types[Money], tc.right, false)
		if err != nil || got != tc.want {
			t.Fatal(got, err, tc.want)
		}
	}
	for _, op := range []ArithmeticOperator{ArithmeticAdd, ArithmeticSubtract} {
		if _, err := ResolveArithmetic(op, types[Money], nil, true); err == nil {
			t.Fatal("money unary admitted")
		}
	}
	p, _ := BindArithmetic(ArithmeticDivide, types[Money], types[Money], types[Float8])
	a, _ := MoneyValue(types[Money], 12300)
	b, _ := MoneyValue(types[Money], 200)
	v, err := p.Binary(ctx, a, b, InputEnvironment{})
	if err != nil || v.floating != 61.5 || v.typ != types[Float8] {
		t.Fatal(v, err)
	}
	v, err = p.Binary(ctx, a, Null(types[Money]), InputEnvironment{})
	if err != nil || !v.null || v.typ != types[Float8] {
		t.Fatal(v, err)
	}
	canceled, cancel := context.WithCancel(ctx)
	cancel()
	if _, err := p.Binary(canceled, a, b, InputEnvironment{}); !errors.Is(err, context.Canceled) {
		t.Fatal(err)
	}
}

func TestMoneyDefaultOrderingProvider(t *testing.T) {
	types := testTypes()
	class, ok := LookupIndexClass("money_ops")
	if !ok || class.OID() != 10047 || class.Name != "money_ops" || class.Family != "money_ops" || class.input != Money || class.IsHash() || !class.Accepts(types[Money]) || class.Accepts(types[Int8]) {
		t.Fatal("money class identity/admission", class, ok)
	}
	defaultClass, ok := DefaultIndexClass(types[Money])
	if !ok || defaultClass != class {
		t.Fatal("money default class", defaultClass, ok)
	}
	comparison, err := BindOrdering(types[Money].Usage())
	if err != nil || comparison.DefaultBtreeOpclass() != 10047 {
		t.Fatal("money ordering provider", comparison, err)
	}
	minimum, _ := MoneyValue(types[Money], math.MinInt64)
	maximum, _ := MoneyValue(types[Money], math.MaxInt64)
	ctx := context.Background()
	for _, tc := range []struct {
		left, right Value
		want        int
	}{{minimum, maximum, -1}, {maximum, minimum, 1}, {minimum, minimum, 0}} {
		got, err := comparison.Compare(ctx, tc.left, tc.right, InputEnvironment{})
		if err != nil || got != tc.want {
			t.Fatal("raw monetary ordering", got, err, tc.want)
		}
	}
}
