package sqltype

import (
	"context"
	"errors"
	"testing"

	"stuntgres/internal/sqlstate"
)

func TestCompareOpsProvidersAndStrictNull(t *testing.T) {
	types := testTypes()
	ctx := context.Background()
	for _, tc := range []struct {
		name        string
		left, right ID
		a, b        string
		sign        int
	}{
		{"int2-int4", Int2, Int4, "-32768", "2147483647", -1},
		{"int4-int8", Int4, Int8, "2147483647", "2147483648", -1},
		{"minimum", Int8, Int4, "-9223372036854775808", "-2147483648", -1},
		{"maximum", Int8, Int4, "9223372036854775807", "2147483647", 1},
		{"exact-integers", Int8, Int8, "9007199254740993", "9007199254740992", 1},
		{"text", Text, Text, "z", "é", -1}, {"name", Name, Name, "a", "b", -1},
		{"boolean", Bool, Bool, "false", "true", -1},
		{"float-nan", Float4, Float8, "NaN", "Infinity", 1},
		{"float-nan-equal", Float8, Float4, "NaN", "NaN", 0},
		{"float-zero", Float4, Float8, "-0", "0", 0},
		{"numeric-exact", Numeric, Int8, "9007199254740993", "9007199254740992", 1},
		{"numeric-scale", Numeric, Numeric, "1.000", "1", 0},
		{"numeric-negative-infinity", Numeric, Numeric, "-Infinity", "0", -1},
		{"numeric-infinity", Numeric, Numeric, "0", "Infinity", -1},
		{"numeric-nan", Numeric, Numeric, "Infinity", "NaN", -1},
		{"numeric-nan-equal", Numeric, Numeric, "NaN", "NaN", 0},
		{"numeric-float", Numeric, Float8, "2", "1", 1},
		{"uuid", UUID, UUID, "7fffffffffffffffffffffffffffffff", "80000000000000000000000000000000", -1},
		{"time", Time, Time, "01:00:00", "02:00:00", -1},
		{"timestamp", Timestamp, Timestamp, "2000-01-01", "2000-01-02", -1},
	} {
		t.Run(tc.name, func(t *testing.T) {
			input := func(id ID, text string) (Value, error) {
				if id == Name {
					return String(types[id], text), nil
				}
				return types[id].Input(ctx, text, -1)
			}
			a, err := input(tc.left, tc.a)
			if err != nil {
				t.Fatal(err)
			}
			b, err := input(tc.right, tc.b)
			if err != nil {
				t.Fatal(err)
			}
			for _, reverse := range []bool{false, true} {
				left, right, sign := a, b, tc.sign
				if reverse {
					left, right, sign = b, a, -tc.sign
				}
				for op, want := range []bool{sign == 0, sign != 0, sign < 0, sign <= 0, sign > 0, sign >= 0} {
					p, err := BindComparisonPredicate(ComparisonOperator(op), left.Type(), right.Type(), types[Bool], left.Type().Usage().Collation, false)
					if err != nil {
						t.Fatal(err)
					}
					result, err := p.Evaluate(ctx, left, right, InputEnvironment{})
					if err != nil || result.Type() != types[Bool] || result.IsNull() {
						t.Fatal(result, err)
					}
					truth, err := result.Boolean()
					if err != nil || truth != want {
						t.Fatal(op, truth, want, err)
					}
					for _, pair := range [][2]Value{{Null(left.Type()), right}, {left, Null(right.Type())}, {Null(left.Type()), Null(right.Type())}} {
						v, err := p.Evaluate(ctx, pair[0], pair[1], InputEnvironment{})
						if err != nil || !v.IsNull() || v.Type() != types[Bool] {
							t.Fatal(v, err)
						}
					}
				}
			}
		})
	}
}

func TestCompareOpsPredicateInvariantsAndCancellation(t *testing.T) {
	types := testTypes()
	for _, tc := range []struct {
		op                  ComparisonOperator
		left, right, result *Descriptor
	}{
		{ComparisonOperator(255), types[Int4], types[Int8], types[Bool]},
		{CompareEqual, nil, types[Int8], types[Bool]},
		{CompareEqual, types[Unknown], types[Int8], types[Bool]},
		{CompareEqual, types[Int4], types[Int8], nil},
		{CompareEqual, types[Int4], types[Int8], types[Text]},
	} {
		_, err := BindComparisonPredicate(tc.op, tc.left, tc.right, tc.result, CollationRef{}, false)
		var diagnostic *sqlstate.Error
		if err == nil || errors.As(err, &diagnostic) {
			t.Fatal("invariant acquired SQLSTATE", err)
		}
	}
	for _, pair := range [][2]ID{{Int4, Bool}, {Text, Int4}, {Time, Timestamp}, {Name, Bool}} {
		_, err := BindComparisonPredicate(CompareLess, types[pair[0]], types[pair[1]], types[Bool], types[pair[0]].Usage().Collation, false)
		var diagnostic *sqlstate.Error
		if !errors.As(err, &diagnostic) || diagnostic.Code != "42883" {
			t.Fatal(err)
		}
	}
	p, err := BindComparisonPredicate(CompareLess, types[Int4], types[Int8], types[Bool], CollationRef{}, false)
	if err != nil {
		t.Fatal(err)
	}
	a, _ := Integer(types[Int4], 1)
	b, _ := Integer(types[Int8], 2)
	for _, pair := range [][2]Value{{{}, b}, {a, {}}, {Null(types[Text]), b}, {a, Null(types[Int4])}} {
		_, err := p.Evaluate(context.Background(), pair[0], pair[1], InputEnvironment{})
		var diagnostic *sqlstate.Error
		if err == nil || errors.As(err, &diagnostic) {
			t.Fatal(err)
		}
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	for _, pair := range [][2]Value{{a, b}, {Null(a.Type()), b}, {a, Null(b.Type())}, {Null(a.Type()), Null(b.Type())}} {
		if _, err := p.Evaluate(ctx, pair[0], pair[1], InputEnvironment{}); !errors.Is(err, context.Canceled) {
			t.Fatal(err)
		}
	}
	if _, err := (ComparisonPredicate{}).Evaluate(context.Background(), Value{}, Value{}, InputEnvironment{}); err == nil {
		t.Fatal("zero predicate accepted")
	}
}
