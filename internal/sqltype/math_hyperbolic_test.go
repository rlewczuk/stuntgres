package sqltype

import (
	"context"
	"errors"
	"math"
	"reflect"
	"testing"

	"stuntgres/internal/sqlstate"
)

func TestMathHyperbolicSignatures(t *testing.T) {
	types, foreign := mathTypes(), mathTypes()
	u, other := types[Float8].Usage(), foreign[Float8].Usage()
	for _, name := range []string{"sinh", "cosh", "tanh", "asinh", "acosh", "atanh"} {
		t.Run(name, func(t *testing.T) {
			plan, err := BindMath(name, u, u)
			if err != nil || plan.ResultUsage() != u {
				t.Fatal(plan, err)
			}
			for _, bad := range []struct {
				result Usage
				args   []Usage
			}{
				{u, nil}, {u, []Usage{u, u}}, {u, []Usage{types[Float4].Usage()}},
				{u, []Usage{types[Numeric].Usage()}}, {other, []Usage{u}},
				{u, []Usage{other}}, {types[Float4].Usage(), []Usage{u}},
				{Usage{Type: u.Type, Typmod: 4}, []Usage{u}},
				{u, []Usage{{Type: u.Type, Typmod: 4}}}, {u, []Usage{{}}},
			} {
				if _, err := BindMath(name, bad.result, bad.args...); err == nil {
					t.Fatal("accepted invalid usage", bad)
				}
			}
			got, err := plan.Evaluate(context.Background(), Null(u.Type), Value{})
			if err != nil || got != Null(u.Type) {
				t.Fatal("strict typed NULL", got, err)
			}
			ctx, cancel := context.WithCancel(context.Background())
			cancel()
			a, _ := Float(u.Type, 0)
			for _, input := range []Value{a, Null(u.Type)} {
				got, err := plan.Evaluate(ctx, input, Value{})
				if !errors.Is(err, context.Canceled) || got.typ != nil {
					t.Fatal("cancellation before NULL/domain", got, err)
				}
				var diagnostic *sqlstate.Error
				if errors.As(err, &diagnostic) {
					t.Fatal("ordinary cancellation acquired SQLSTATE", err)
				}
			}
			foreignValue, _ := Float(other.Type, 1)
			for _, input := range []Value{foreignValue, {}} {
				if _, err := plan.Evaluate(context.Background(), input, Value{}); err == nil {
					t.Fatal("accepted invalid value owner", input)
				}
			}
		})
	}
}

func TestMathHyperbolicValues(t *testing.T) {
	d := mathTypes()[Float8]
	cases := []struct {
		name        string
		input, want float64
		ulps        uint64
	}{
		{"sinh", 1, 1.1752011936438014, 2}, {"cosh", 0, 1, 0}, {"tanh", 1, 0.7615941559557649, 2},
		{"asinh", 1, 0.881373587019543, 2}, {"acosh", 1, 0, 0}, {"atanh", 0.5, 0.5493061443340548, 2},
		{"acosh", 2, 1.3169578969248166, 2}, {"acosh", math.Nextafter(1, 2), 2.1073424255447017e-8, 2},
		{"atanh", math.Nextafter(1, 0), 18.714973875118524, 2},
		{"acosh", math.MaxFloat64, 710.4758600739439, 2},
	}
	for _, name := range []string{"sinh", "cosh", "tanh", "asinh", "acosh", "atanh"} {
		cases = append(cases, struct {
			name        string
			input, want float64
			ulps        uint64
		}{name, math.NaN(), math.NaN(), 0})
	}
	for _, name := range []string{"sinh", "cosh", "tanh", "asinh", "atanh"} {
		for _, sign := range []float64{1, -1} {
			for _, input := range []float64{math.Copysign(0, sign), sign * math.SmallestNonzeroFloat64} {
				want := input
				if name == "cosh" {
					want = 1
				}
				cases = append(cases, struct {
					name        string
					input, want float64
					ulps        uint64
				}{name, input, want, 0})
			}
		}
	}
	for _, name := range []string{"sinh", "cosh"} {
		for _, sign := range []float64{1, -1} {
			for _, tc := range []struct{ input, want float64 }{{709, 4.109203730777486e307}, {710, 1.1169973830808555e308}, {711, math.Inf(1)}, {1000, math.Inf(1)}, {math.Inf(1), math.Inf(1)}} {
				want := tc.want
				if name == "sinh" {
					want *= sign
				}
				cases = append(cases, struct {
					name        string
					input, want float64
					ulps        uint64
				}{name, sign * tc.input, want, 2})
			}
		}
	}
	for _, sign := range []float64{1, -1} {
		cases = append(cases, struct {
			name        string
			input, want float64
			ulps        uint64
		}{"atanh", sign, math.Inf(int(sign)), 0})
	}
	for _, tc := range cases {
		plan, err := BindMath(tc.name, d.Usage(), d.Usage())
		if err != nil {
			t.Fatal(err)
		}
		a, _ := Float(d, tc.input)
		got, err := plan.Evaluate(context.Background(), a, Value{})
		if err != nil || got.typ != d || !mathTrigMatches(got.floating, tc.want, tc.ulps) {
			t.Fatalf("%s(%g) got=%g want=%g err=%v", tc.name, tc.input, got.floating, tc.want, err)
		}
	}
}

func TestMathHyperbolicDomains(t *testing.T) {
	d := mathTypes()[Float8]
	want := &sqlstate.Error{Severity: "ERROR", SeverityUnlocalized: "ERROR", Code: "22003", Message: "input is out of range"}
	for _, tc := range []struct {
		name   string
		inputs []float64
	}{
		{"acosh", []float64{math.Inf(-1), -1, math.Copysign(0, -1), 0, math.Nextafter(1, 0)}},
		{"atanh", []float64{math.Inf(-1), math.Inf(1), -2, 2, math.Nextafter(-1, -2), math.Nextafter(1, 2)}},
	} {
		plan, err := BindMath(tc.name, d.Usage(), d.Usage())
		if err != nil {
			t.Fatal(err)
		}
		for _, input := range tc.inputs {
			a, _ := Float(d, input)
			_, err := plan.Evaluate(context.Background(), a, Value{})
			var diagnostic *sqlstate.Error
			if !errors.As(err, &diagnostic) || !reflect.DeepEqual(diagnostic, want) {
				t.Fatal(tc.name, input, err)
			}
		}
	}
}
