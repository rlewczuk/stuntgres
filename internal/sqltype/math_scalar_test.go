package sqltype

import (
	"context"
	"errors"
	"math"
	"math/big"
	"strings"
	"testing"

	"stuntgres/internal/sqlstate"
)

func TestMathScalarSignatures(t *testing.T) {
	types := mathTypes()
	for _, name := range []string{"abs", "ceil", "ceiling", "floor", "exp", "div", "factorial"} {
		ids := []ID{Numeric, Float8}
		if name == "abs" {
			ids = []ID{Int2, Int4, Int8, Float4, Float8, Numeric}
		}
		if name == "div" {
			ids = []ID{Numeric}
		}
		if name == "factorial" {
			ids = []ID{Int8}
		}
		for _, id := range ids {
			t.Run(name+"/"+types[id].Name(), func(t *testing.T) {
				result := types[id]
				if name == "factorial" {
					result = types[Numeric]
				}
				args := []Usage{types[id].Usage()}
				if name == "div" {
					args = append(args, args[0])
				}
				p, err := BindMath(name, result.Usage(), args...)
				if err != nil {
					t.Fatal(err)
				}
				if p.Name() != name || p.ResultUsage() != result.Usage() {
					t.Fatal(p)
				}
				for i := range args {
					var values [2]Value
					for j, u := range args {
						values[j], err = u.Type.Input(context.Background(), "1", -1)
						if err != nil {
							t.Fatal(err)
						}
					}
					values[i] = Null(args[i].Type)
					got, err := p.Evaluate(context.Background(), values[0], values[1])
					if err != nil || got != Null(result) {
						t.Fatal(got, err)
					}
				}
				if _, err := BindMath(name, result.Usage()); err == nil {
					t.Fatal("accepted zero arity")
				}
				if _, err := BindMath(name, types[Text].Usage(), args...); err == nil {
					t.Fatal("accepted wrong result")
				}
				foreign := *types[id]
				bad := append([]Usage(nil), args...)
				bad[0] = foreign.Usage()
				if name != "factorial" {
					if _, err := BindMath(name, result.Usage(), bad...); err == nil {
						t.Fatal("accepted foreign owner")
					}
				}
				bad[0] = types[Text].Usage()
				if _, err := BindMath(name, result.Usage(), bad...); err == nil {
					t.Fatal("accepted TEXT")
				}
			})
		}
	}
}

func scalarProvider(t *testing.T, name string, id ID, inputs ...string) (MathPlan, [2]Value) {
	t.Helper()
	types := mathTypes()
	result := types[id]
	if name == "factorial" {
		result = types[Numeric]
	}
	var args []Usage
	var values [2]Value
	for i, input := range inputs {
		args = append(args, types[id].Usage())
		var err error
		values[i], err = types[id].Input(context.Background(), input, -1)
		if err != nil {
			t.Fatal(input, err)
		}
	}
	p, err := BindMath(name, result.Usage(), args...)
	if err != nil {
		t.Fatal(name, err)
	}
	return p, values
}

func TestMathScalarNumericValues(t *testing.T) {
	for _, tc := range []struct{ name, a, b, want string }{
		{"abs", "-17.400", "", "17.400"}, {"abs", "-0.000", "", "0.000"}, {"abs", "-Infinity", "", "Infinity"}, {"abs", "NaN", "", "NaN"},
		{"ceil", "42.8", "", "43"}, {"ceiling", "-42.8", "", "-42"}, {"floor", "-42.8", "", "-43"},
		{"ceil", "-0.0001", "", "0"}, {"ceiling", "0.0001", "", "1"}, {"floor", "-0.0001", "", "-1"},
		{"ceil", "42.000", "", "42"}, {"floor", "42.000", "", "42"},
		{"div", "-9.00", "4.000", "-2"}, {"div", "9.00", "-4.000", "-2"}, {"div", "-9.00", "-4.000", "2"},
		{"div", "1.00", "2.0", "0"}, {"div", "9007199254740993", "2", "4503599627370496"}, {"div", "12345678901234567890", "123", "100371373180768844"},
		{"exp", "0.0", "", "1.0000000000000000"}, {"exp", "1.0", "", "2.7182818284590452"}, {"exp", "-1.0", "", "0.3678794411714423"},
		{"exp", "1." + strings.Repeat("0", 70), "", "2.7182818284590452353602874713526624977572470936999595749669676277240766"},
		{"exp", "-6000", "", "0." + strings.Repeat("0", 1000)}, {"exp", "-Infinity", "", "0"}, {"exp", "Infinity", "", "Infinity"}, {"exp", "NaN", "", "NaN"},
		{"exp", "0." + strings.Repeat("0", 1001), "", "1." + strings.Repeat("0", 1000)},
	} {
		t.Run(tc.name+"/"+tc.a, func(t *testing.T) {
			inputs := []string{tc.a}
			if tc.b != "" {
				inputs = append(inputs, tc.b)
			}
			p, v := scalarProvider(t, tc.name, Numeric, inputs...)
			original := v
			got, err := p.Evaluate(context.Background(), v[0], v[1])
			if err != nil {
				t.Fatal(err)
			}
			text, err := p.ResultUsage().Type.Output(context.Background(), got)
			if err != nil || text != tc.want || v != original {
				t.Fatalf("got %q want %q: %v", text, tc.want, err)
			}
		})
	}
	for _, name := range []string{"ceil", "ceiling", "floor"} {
		for _, special := range []string{"NaN", "Infinity", "-Infinity"} {
			p, v := scalarProvider(t, name, Numeric, special)
			got, err := p.Evaluate(context.Background(), v[0], v[1])
			if err != nil || got != v[0] {
				t.Fatal(name, special, got, err)
			}
		}
	}
	for _, a := range []string{"NaN", "Infinity", "-Infinity", "0", "2", "-2"} {
		for _, b := range []string{"NaN", "Infinity", "-Infinity", "0", "2", "-2"} {
			p, v := scalarProvider(t, "div", Numeric, a, b)
			got, err := p.Evaluate(context.Background(), v[0], v[1])
			if b == "0" && a != "NaN" {
				scalarError(t, err, "22012", "division by zero")
				continue
			}
			want := "0"
			if a == "NaN" || b == "NaN" || (strings.Contains(a, "Infinity") && strings.Contains(b, "Infinity")) {
				want = "NaN"
			} else if strings.Contains(a, "Infinity") {
				want = "Infinity"
				if strings.HasPrefix(a, "-") != strings.HasPrefix(b, "-") {
					want = "-Infinity"
				}
			} else if !strings.Contains(b, "Infinity") && a != "0" {
				want = "1"
				if strings.HasPrefix(a, "-") != strings.HasPrefix(b, "-") {
					want = "-1"
				}
			}
			if err != nil {
				t.Fatal(a, b, err)
			}
			text, e := p.ResultUsage().Type.Output(context.Background(), got)
			if e != nil || text != want {
				t.Fatal(a, b, text, want, e)
			}
		}
	}
}

func scalarError(t *testing.T, err error, code, message string) {
	t.Helper()
	var got *sqlstate.Error
	want := sqlstate.Error{Severity: "ERROR", SeverityUnlocalized: "ERROR", Code: code, Message: message}
	if !errors.As(err, &got) || *got != want {
		t.Fatalf("got %#v (%v), want %#v", got, err, want)
	}
}

func TestMathScalarErrorsAndBounds(t *testing.T) {
	for _, tc := range []struct {
		name                string
		id                  ID
		a, b, code, message string
	}{
		{"abs", Int2, "-32768", "", "22003", "smallint out of range"}, {"abs", Int4, "-2147483648", "", "22003", "integer out of range"}, {"abs", Int8, "-9223372036854775808", "", "22003", "bigint out of range"},
		{"factorial", Int8, "-1", "", "22003", "factorial of a negative number is undefined"}, {"factorial", Int8, "32178", "", "22003", "value overflows numeric format"},
		{"div", Numeric, "1", "0", "22012", "division by zero"}, {"div", Numeric, "Infinity", "0", "22012", "division by zero"}, {"div", Numeric, "1e131071", "0.1", "22003", "value overflows numeric format"},
		{"exp", Numeric, "6000", "", "22003", "value overflows numeric format"}, {"exp", Float8, "710", "", "22003", "value out of range: overflow"}, {"exp", Float8, "-746", "", "22003", "value out of range: underflow"},
		{"ceil", Numeric, strings.Repeat("9", 131072) + ".1", "", "22003", "value overflows numeric format"}, {"ceiling", Numeric, strings.Repeat("9", 131072) + ".1", "", "22003", "value overflows numeric format"}, {"floor", Numeric, "-" + strings.Repeat("9", 131072) + ".1", "", "22003", "value overflows numeric format"},
	} {
		t.Run(tc.name+"/"+tc.message, func(t *testing.T) {
			inputs := []string{tc.a}
			if tc.b != "" {
				inputs = append(inputs, tc.b)
			}
			p, v := scalarProvider(t, tc.name, tc.id, inputs...)
			_, err := p.Evaluate(context.Background(), v[0], v[1])
			scalarError(t, err, tc.code, tc.message)
		})
	}
}

func scalarProduct(lo, hi int64) *big.Int {
	if lo > hi {
		return big.NewInt(1)
	}
	if lo == hi {
		return big.NewInt(lo)
	}
	mid := (lo + hi) / 2
	return new(big.Int).Mul(scalarProduct(lo, mid), scalarProduct(mid+1, hi))
}

func TestMathScalarFactorial(t *testing.T) {
	for _, tc := range []struct{ input, want string }{{"0", "1"}, {"1", "1"}, {"5", "120"}, {"20", "2432902008176640000"}, {"32177", scalarProduct(2, 32177).String()}} {
		t.Run(tc.input, func(t *testing.T) {
			p, v := scalarProvider(t, "factorial", Int8, tc.input)
			got, err := p.Evaluate(context.Background(), v[0], v[1])
			if err != nil {
				t.Fatal(err)
			}
			text, err := p.ResultUsage().Type.Output(context.Background(), got)
			if err != nil || text != tc.want {
				t.Fatal("factorial mismatch", len(text), len(tc.want), err)
			}
		})
	}
}

func TestMathScalarFloatAndInteger(t *testing.T) {
	for _, id := range []ID{Int2, Int4, Int8} {
		for _, tc := range []struct {
			input string
			want  int64
		}{{"-17", 17}, {"0", 0}, {"17", 17}} {
			p, v := scalarProvider(t, "abs", id, tc.input)
			got, err := p.Evaluate(context.Background(), v[0], v[1])
			if err != nil || got.integer != tc.want {
				t.Fatal(id, tc, got, err)
			}
		}
	}
	for _, id := range []ID{Float4, Float8} {
		for _, tc := range []struct {
			input string
			want  float64
		}{{"-17.5", 17.5}, {"-0", 0}, {"-Infinity", math.Inf(1)}, {"Infinity", math.Inf(1)}, {"NaN", math.NaN()}} {
			p, v := scalarProvider(t, "abs", id, tc.input)
			got, err := p.Evaluate(context.Background(), v[0], v[1])
			if err != nil || math.IsNaN(tc.want) != math.IsNaN(got.floating) || !math.IsNaN(tc.want) && (got.floating != tc.want || math.Signbit(got.floating)) {
				t.Fatal(id, tc, got, err)
			}
		}
	}
	for _, tc := range []struct {
		name, input string
		want        float64
	}{{"abs", "-0", 0}, {"ceil", "-0.5", math.Copysign(0, -1)}, {"ceiling", "-0.5", math.Copysign(0, -1)}, {"floor", "-0", math.Copysign(0, -1)}, {"ceil", "42.8", 43}, {"floor", "-42.8", -43}, {"exp", "0", 1}, {"exp", "-Infinity", 0}, {"exp", "Infinity", math.Inf(1)}, {"exp", "NaN", math.NaN()}} {
		p, v := scalarProvider(t, tc.name, Float8, tc.input)
		got, err := p.Evaluate(context.Background(), v[0], v[1])
		if err != nil || math.IsNaN(tc.want) != math.IsNaN(got.floating) || !math.IsNaN(tc.want) && (got.floating != tc.want || math.Signbit(got.floating) != math.Signbit(tc.want)) {
			t.Fatal(tc, got, err)
		}
	}
	p, v := scalarProvider(t, "exp", Float8, "1")
	got, err := p.Evaluate(context.Background(), v[0], v[1])
	if err != nil || math.Abs(got.floating-2.718281828459045) > 2*4.440892098500626e-16 {
		t.Fatal(got, err)
	}
}

func TestMathScalarCancellation(t *testing.T) {
	for _, tc := range []struct {
		name   string
		id     ID
		inputs []string
	}{{"factorial", Int8, []string{"32177"}}, {"exp", Numeric, []string{"5999.00"}}, {"div", Numeric, []string{"1e131071", "0.3"}}} {
		p, v := scalarProvider(t, tc.name, tc.id, tc.inputs...)
		for _, checkpoint := range []int{1, 2, 3, 4} {
			ctx := &mathCheckpointContext{Context: context.Background(), remaining: checkpoint}
			_, err := p.Evaluate(ctx, v[0], v[1])
			if !errors.Is(err, context.Canceled) {
				t.Fatal(tc.name, checkpoint, err)
			}
		}
	}
}
