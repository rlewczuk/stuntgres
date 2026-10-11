package sqltype

import (
	"context"
	"errors"
	"math"
	"reflect"
	"testing"

	"stuntgres/internal/sqlstate"
)

func TestMathTrigSignatures(t *testing.T) {
	types, foreign := mathTypes(), mathTypes()
	u, other := types[Float8].Usage(), foreign[Float8].Usage()
	for _, name := range []string{"acos", "acosd", "asin", "asind", "atan", "atand", "atan2", "atan2d"} {
		t.Run(name, func(t *testing.T) {
			args := []Usage{u}
			binary := name == "atan2" || name == "atan2d"
			if binary {
				args = append(args, u)
			}
			plan, err := BindMath(name, u, args...)
			if err != nil || plan.Name() != name || plan.ResultUsage() != u {
				t.Fatal(plan, err)
			}
			bad := []struct {
				result Usage
				args   []Usage
			}{
				{u, nil}, {u, []Usage{u, u, u}}, {types[Float4].Usage(), args}, {other, args},
				{Usage{Type: u.Type, Typmod: 4}, args}, {u, []Usage{types[Float4].Usage()}},
				{u, []Usage{types[Numeric].Usage()}}, {u, []Usage{{}}},
				{u, []Usage{{Type: u.Type, Typmod: 4}}},
			}
			if binary {
				bad = append(bad, struct {
					result Usage
					args   []Usage
				}{u, []Usage{u}})
				for _, second := range []Usage{other, types[Float4].Usage(), Usage{Type: u.Type, Typmod: 4}, {}} {
					bad = append(bad, struct {
						result Usage
						args   []Usage
					}{u, []Usage{u, second}})
				}
				bad = append(bad, struct {
					result Usage
					args   []Usage
				}{u, []Usage{other, u}})
			} else {
				bad = append(bad, struct {
					result Usage
					args   []Usage
				}{u, []Usage{u, u}})
				bad = append(bad, struct {
					result Usage
					args   []Usage
				}{u, []Usage{other}})
			}
			for _, tc := range bad {
				if _, err := BindMath(name, tc.result, tc.args...); err == nil {
					t.Fatal("accepted invalid signature", tc)
				}
			}
			a, _ := Float(u.Type, 0.5)
			for _, pair := range [][2]Value{{Null(u.Type), a}, {a, Null(u.Type)}} {
				if !binary && !pair[1].null {
					pair[1] = Value{}
				}
				if !binary && !pair[0].null {
					continue
				}
				got, err := plan.Evaluate(context.Background(), pair[0], pair[1])
				if err != nil || got != Null(u.Type) {
					t.Fatal("strict typed NULL", got, err)
				}
			}
			ctx, cancel := context.WithCancel(context.Background())
			cancel()
			got, err := plan.Evaluate(ctx, a, a)
			if !errors.Is(err, context.Canceled) || got.typ != nil {
				t.Fatal("ordinary cancellation", got, err)
			}
			var diagnostic *sqlstate.Error
			if errors.As(err, &diagnostic) {
				t.Fatal("cancellation acquired SQLSTATE", err)
			}
			foreignValue, _ := Float(other.Type, 0.5)
			if _, err := plan.Evaluate(context.Background(), foreignValue, a); err == nil {
				t.Fatal("foreign first value")
			}
			if binary {
				if _, err := plan.Evaluate(context.Background(), a, foreignValue); err == nil {
					t.Fatal("foreign second value")
				}
				if _, err := plan.Evaluate(context.Background(), a, Value{}); err == nil {
					t.Fatal("missing second value")
				}
			}
			if _, err := plan.Evaluate(context.Background(), Value{}, a); err == nil {
				t.Fatal("missing first value")
			}
		})
	}
	if _, err := (MathPlan{}).Evaluate(context.Background(), Value{}, Value{}); err == nil {
		t.Fatal("zero plan")
	}
}

func mathTrigMatches(got, want float64, ulps uint64) bool {
	if math.IsNaN(want) {
		return math.IsNaN(got)
	}
	if math.Signbit(got) != math.Signbit(want) {
		return false
	}
	if got == want {
		return true
	}
	if math.IsNaN(got) || math.IsInf(got, 0) || math.IsInf(want, 0) {
		return false
	}
	a, b := math.Float64bits(got), math.Float64bits(want)
	if a > b {
		return a-b <= ulps
	}
	return b-a <= ulps
}

func TestMathTrigValues(t *testing.T) {
	d := mathTypes()[Float8]
	negativeZero := math.Copysign(0, -1)
	cases := []struct {
		name       string
		a, b, want float64
		ulps       uint64
	}{
		{"acos", -1, 0, 3.141592653589793, 2}, {"asin", 1, 0, 1.5707963267948966, 2}, {"atan", 1, 0, 0.7853981633974483, 2},
		{"acos", 0.25, 0, 1.318116071652818, 2}, {"asin", 0.25, 0, 0.25268025514207865, 2}, {"atan", 0.25, 0, 0.24497866312686414, 2},
		{"asind", -1, 0, -90, 0}, {"asind", -0.5, 0, -30, 0}, {"asind", 0.5, 0, 30, 0}, {"asind", 1, 0, 90, 0},
		{"acosd", -1, 0, 180, 0}, {"acosd", -0.5, 0, 120, 0}, {"acosd", 0, 0, 90, 0}, {"acosd", 0.5, 0, 60, 0}, {"acosd", 1, 0, 0, 0},
		{"atand", math.Inf(-1), 0, -90, 0}, {"atand", -1, 0, -45, 0}, {"atand", 1, 0, 45, 0}, {"atand", math.Inf(1), 0, 90, 0},
		{"asind", 0.25, 0, 14.477512185929925, 2}, {"acosd", 0.75, 0, 41.40962210927086, 2}, {"atand", 0.25, 0, 14.036243467926479, 2},
		{"asind", math.Nextafter(0.5, 0), 0, 29.999999999999996, 2}, {"asind", math.Nextafter(0.5, 1), 0, 30.000000000000007, 2},
		{"atan2d", 1, 1, 45, 0}, {"atan2d", 1, -1, 135, 0}, {"atan2d", -1, -1, -135, 0}, {"atan2d", -1, 1, -45, 0},
		{"atan2d", 0, 10, 0, 0}, {"atan2d", 10, 0, 90, 0}, {"atan2d", 0, -10, 180, 0}, {"atan2d", -10, 0, -90, 0},
		{"atan2d", 0, 0, 0, 0}, {"atan2d", negativeZero, 0, negativeZero, 0}, {"atan2d", 0, negativeZero, 180, 0}, {"atan2d", negativeZero, negativeZero, -180, 0},
		{"atan2", negativeZero, 0, negativeZero, 0}, {"atan2", 0, negativeZero, 3.141592653589793, 2}, {"atan2", negativeZero, negativeZero, -3.141592653589793, 2},
		{"atan2d", math.Inf(1), math.Inf(1), 45, 0}, {"atan2d", math.Inf(1), math.Inf(-1), 135, 0}, {"atan2d", math.Inf(-1), math.Inf(-1), -135, 0}, {"atan2d", math.Inf(-1), math.Inf(1), -45, 0},
		{"atan2d", -1, math.Inf(1), negativeZero, 0}, {"atan2d", -1, math.Inf(-1), -180, 0},
	}
	for _, name := range []string{"asin", "asind", "atan", "atand"} {
		want := math.SmallestNonzeroFloat64
		if name == "asind" {
			want *= 60
		}
		if name == "atand" {
			want *= 45
		}
		cases = append(cases, struct {
			name       string
			a, b, want float64
			ulps       uint64
		}{name, negativeZero, 0, negativeZero, 0})
		for _, sign := range []float64{1, -1} {
			cases = append(cases, struct {
				name       string
				a, b, want float64
				ulps       uint64
			}{name, sign * math.SmallestNonzeroFloat64, 0, sign * want, 0})
		}
	}
	for _, tc := range cases {
		args := []Usage{d.Usage()}
		if tc.name == "atan2" || tc.name == "atan2d" {
			args = append(args, d.Usage())
		}
		plan, err := BindMath(tc.name, d.Usage(), args...)
		if err != nil {
			t.Fatal(err)
		}
		a, _ := Float(d, tc.a)
		b, _ := Float(d, tc.b)
		beforeA, beforeB := math.Float64bits(a.floating), math.Float64bits(b.floating)
		v, err := plan.Evaluate(context.Background(), a, b)
		if err != nil || v.typ != d || v.null || !mathTrigMatches(v.floating, tc.want, tc.ulps) {
			t.Fatalf("%s(%g,%g): actual %g bits%x want %g bits%x err%v", tc.name, tc.a, tc.b, v.floating, math.Float64bits(v.floating), tc.want, math.Float64bits(tc.want), err)
		}
		if math.Float64bits(a.floating) != beforeA || math.Float64bits(b.floating) != beforeB {
			t.Fatal("changed inputs")
		}
	}
}

func TestMathTrigDomainAndNaN(t *testing.T) {
	d := mathTypes()[Float8]
	ctx := context.Background()
	for _, name := range []string{"acos", "acosd", "asin", "asind"} {
		plan, err := BindMath(name, d.Usage(), d.Usage())
		if err != nil {
			t.Fatal(err)
		}
		for _, input := range []float64{-2, 2, math.Inf(-1), math.Inf(1), math.Nextafter(-1, math.Inf(-1)), math.Nextafter(1, math.Inf(1))} {
			a, _ := Float(d, input)
			v, err := plan.Evaluate(ctx, a, Value{})
			var diagnostic *sqlstate.Error
			want := &sqlstate.Error{Severity: "ERROR", SeverityUnlocalized: "ERROR", Code: "22003", Message: "input is out of range"}
			if v.typ != nil || !errors.As(err, &diagnostic) || !reflect.DeepEqual(diagnostic, want) {
				t.Fatal(name, input, v, err)
			}
		}
		for _, input := range []float64{-1, 1, math.Nextafter(-1, 0), math.Nextafter(1, 0)} {
			a, _ := Float(d, input)
			v, err := plan.Evaluate(ctx, a, Value{})
			if err != nil || v.typ != d || v.null || math.IsNaN(v.floating) || math.IsInf(v.floating, 0) {
				t.Fatal("inside boundary", name, input, v, err)
			}
		}
	}
	for _, name := range []string{"acos", "acosd", "asin", "asind", "atan", "atand", "atan2", "atan2d"} {
		args := []Usage{d.Usage()}
		binary := name == "atan2" || name == "atan2d"
		if binary {
			args = append(args, d.Usage())
		}
		plan, err := BindMath(name, d.Usage(), args...)
		if err != nil {
			t.Fatal(err)
		}
		nan, _ := Float(d, math.NaN())
		infinity, _ := Float(d, math.Inf(1))
		pairs := [][2]Value{{nan, infinity}}
		if binary {
			pairs = append(pairs, [2]Value{infinity, nan}, [2]Value{nan, nan})
		}
		for _, pair := range pairs {
			v, err := plan.Evaluate(ctx, pair[0], pair[1])
			if err != nil || v.typ != d || v.null || !math.IsNaN(v.floating) {
				t.Fatal("NaN propagation", name, v, err)
			}
		}
	}
}
