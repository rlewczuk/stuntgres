package sqltype

import (
	"context"
	"errors"
	"fmt"
	"strconv"
	"strings"
	"testing"
)

func TestMathGcdLcmProviderSignatures(t *testing.T) {
	types := mathTypes()
	for _, name := range []string{"gcd", "lcm"} {
		for _, id := range []ID{Int4, Int8, Numeric} {
			d := types[id]
			u := d.Usage()
			p, err := BindMath(name, u, u, u)
			if err != nil || p.Name() != name || p.ResultUsage() != u {
				t.Fatal(name, id, p, err)
			}
			one, err := d.Input(context.Background(), "1", -1)
			if err != nil {
				t.Fatal(err)
			}
			for _, pair := range [][2]Value{{Null(d), one}, {one, Null(d)}, {Null(d), Null(d)}} {
				got, err := p.Evaluate(context.Background(), pair[0], pair[1])
				if err != nil || got != Null(d) {
					t.Fatal("strict typed NULL", got, err)
				}
			}
			for _, args := range [][]Usage{nil, {u}, {u, u, u}, {u, types[Text].Usage()}, {types[Int2].Usage(), types[Int2].Usage()}, {types[Float8].Usage(), types[Float8].Usage()}, {u, types[Numeric].Usage()}} {
				if id == Numeric && len(args) == 2 && args[0] == u && args[1] == u {
					continue
				}
				if _, err := BindMath(name, u, args...); err == nil {
					t.Fatal("accepted wrong exact signature", name, args)
				}
			}
			constrained := u
			constrained.Typmod = 123
			for _, args := range [][]Usage{{constrained, u}, {u, constrained}} {
				if _, err := BindMath(name, u, args...); err == nil {
					t.Fatal("accepted constrained argument", name)
				}
			}
			if _, err := BindMath(name, constrained, u, u); err == nil {
				t.Fatal("accepted constrained result")
			}
			if _, err := BindMath(name, types[Text].Usage(), u, u); err == nil {
				t.Fatal("accepted wrong result type")
			}
			foreign := *d
			for _, args := range [][]Usage{{foreign.Usage(), u}, {u, foreign.Usage()}, {foreign.Usage(), foreign.Usage()}} {
				if _, err := BindMath(name, u, args...); err == nil {
					t.Fatal("accepted foreign argument owner")
				}
			}
			if _, err := BindMath(name, foreign.Usage(), u, u); err == nil {
				t.Fatal("accepted foreign result owner")
			}
			got, err := p.Evaluate(context.Background(), one, Null(&foreign))
			if err == nil || got != (Value{}) {
				t.Fatal("foreign NULL bypassed descriptor check", got, err)
			}
		}
	}
}

func gcdLcmProviderValue(t *testing.T, name string, id ID, a, b, want string) {
	t.Helper()
	p, inputs := scalarProvider(t, name, id, a, b)
	original := inputs
	got, err := p.Evaluate(context.Background(), inputs[0], inputs[1])
	if err != nil {
		t.Fatal(name, id, a, b, err)
	}
	if got.Type() != p.ResultUsage().Type {
		t.Fatal("result owner", got.Type(), p.ResultUsage())
	}
	text, err := p.ResultUsage().Type.Output(context.Background(), got)
	if err != nil || text != want {
		t.Fatalf("%s(%s,%s)=%s want %s: %v", name, a, b, text, want, err)
	}
	if inputs != original {
		t.Fatal("mutated immutable operands")
	}
	again, err := p.Evaluate(context.Background(), inputs[1], inputs[0])
	if err != nil || again != got || inputs != original {
		t.Fatal("reversed operands/retained result", again, got, err)
	}
}

func TestMathGcdLcmProviderInteger(t *testing.T) {
	for _, w := range []struct {
		id                      ID
		min, max, half, message string
	}{
		{Int4, "-2147483648", "2147483647", "1073741824", "integer out of range"},
		{Int8, "-9223372036854775808", "9223372036854775807", "4611686018427387904", "bigint out of range"},
	} {
		for _, tc := range []struct{ name, a, b, want string }{
			{"gcd", "0", "0", "0"}, {"gcd", w.min, "1", "1"}, {"gcd", w.min, "-1", "1"}, {"gcd", w.min, w.half, w.half}, {"gcd", w.min, "-" + w.half, w.half}, {"gcd", w.min, w.max, "1"},
			{"lcm", w.min, "0", "0"}, {"lcm", "0", "0", "0"}, {"lcm", w.max, w.max, w.max}, {"lcm", "330", "462", "2310"},
		} {
			t.Run(fmt.Sprint(w.id, tc), func(t *testing.T) { gcdLcmProviderValue(t, tc.name, w.id, tc.a, tc.b, tc.want) })
		}
		for _, pair := range [][2]string{{"1071", "462"}, {"-1071", "462"}, {"1071", "-462"}, {"-1071", "-462"}} {
			gcdLcmProviderValue(t, "gcd", w.id, pair[0], pair[1], "21")
			gcdLcmProviderValue(t, "lcm", w.id, pair[0], pair[1], "23562")
		}
		max, _ := strconv.ParseInt(w.max, 10, 64)
		for _, tc := range []struct{ name, a, b string }{
			{"gcd", w.min, "0"}, {"gcd", w.min, w.min}, {"lcm", w.min, "1"}, {"lcm", w.min, "-1"}, {"lcm", w.min, w.min}, {"lcm", w.max, "2"}, {"lcm", strconv.FormatInt(max-1, 10), w.max},
		} {
			for _, pair := range [][2]string{{tc.a, tc.b}, {tc.b, tc.a}} {
				p, v := scalarProvider(t, tc.name, w.id, pair[0], pair[1])
				original := v
				got, err := p.Evaluate(context.Background(), v[0], v[1])
				scalarError(t, err, "22003", w.message)
				if got != (Value{}) || v != original {
					t.Fatal("error published value or mutated operands")
				}
			}
		}
	}
}

func TestMathGcdLcmProviderNumeric(t *testing.T) {
	for _, tc := range []struct{ a, b, g, l string }{
		{"0.00", "0.000", "0.000", "0.000"}, {"0.00", "-2.0000", "2.0000", "0.0000"}, {"-0.00", "-0.000", "0.000", "0.000"},
		{"-1.2", "1.2000", "1.2000", "1.2000"}, {"9007199254740993.00", "3", "3.00", "9007199254740993.00"},
		{"-9223372036854775808.0", "0", "9223372036854775808.0", "0.0"},
		{"1e1000", "1e-1000", "0." + strings.Repeat("0", 999) + "1", "1" + strings.Repeat("0", 1000) + "." + strings.Repeat("0", 1000)},
		{"1e-16383", "2e-16383", "0." + strings.Repeat("0", 16382) + "1", "0." + strings.Repeat("0", 16382) + "2"},
		{"1e131071", "2.0", "2.0", "1" + strings.Repeat("0", 131071) + ".0"},
	} {
		t.Run(fmt.Sprintf("lengths_%d_%d", len(tc.g), len(tc.l)), func(t *testing.T) {
			gcdLcmProviderValue(t, "gcd", Numeric, tc.a, tc.b, tc.g)
			gcdLcmProviderValue(t, "lcm", Numeric, tc.a, tc.b, tc.l)
		})
	}
	for _, signA := range []string{"", "-"} {
		for _, signB := range []string{"", "-"} {
			gcdLcmProviderValue(t, "gcd", Numeric, signA+"4331.250", signB+"463.75000", "8.75000")
			gcdLcmProviderValue(t, "lcm", Numeric, signA+"4232.820", signB+"132.72000", "118518.96000")
			gcdLcmProviderValue(t, "gcd", Numeric, signA+"1.20", signB+"0.1800", "0.0600")
			gcdLcmProviderValue(t, "lcm", Numeric, signA+"1.20", signB+"0.1800", "3.6000")
		}
	}
	for _, special := range []string{"NaN", "Infinity", "-Infinity"} {
		for _, peer := range []string{"NaN", "Infinity", "-Infinity", "0.00", "1.2", "-1.2"} {
			for _, name := range []string{"gcd", "lcm"} {
				gcdLcmProviderValue(t, name, Numeric, special, peer, "NaN")
			}
		}
		for _, name := range []string{"gcd", "lcm"} {
			p, v := scalarProvider(t, name, Numeric, special, "0")
			for _, pair := range [][2]Value{{v[0], Null(v[1].typ)}, {Null(v[0].typ), v[0]}} {
				got, err := p.Evaluate(context.Background(), pair[0], pair[1])
				if err != nil || got != Null(p.ResultUsage().Type) {
					t.Fatal("nonfinite STRICT gate", got, err)
				}
			}
		}
	}
	large := strings.Repeat("9", 131072)
	gcdLcmProviderValue(t, "gcd", Numeric, large, "2", "1")
	p, v := scalarProvider(t, "lcm", Numeric, large, "2")
	original := v
	got, err := p.Evaluate(context.Background(), v[0], v[1])
	scalarError(t, err, "22003", "value overflows numeric format")
	if got != (Value{}) || v != original {
		t.Fatal("numeric overflow publication/mutation")
	}
}

func TestMathGcdLcmProviderCancellation(t *testing.T) {
	for _, name := range []string{"gcd", "lcm"} {
		for _, id := range []ID{Int4, Int8, Numeric} {
			p, v := scalarProvider(t, name, id, "12", "18")
			ctx, cancel := context.WithCancel(context.Background())
			cancel()
			for _, pair := range [][2]Value{{v[0], v[1]}, {Null(v[0].typ), v[1]}} {
				got, err := p.Evaluate(ctx, pair[0], pair[1])
				if got != (Value{}) || !errors.Is(err, context.Canceled) {
					t.Fatal("pre-cancellation", got, err)
				}
				var state interface{ SQLState() string }
				if errors.As(err, &state) {
					t.Fatal("ordinary cancellation acquired SQLSTATE", err)
				}
			}
		}
		p, v := scalarProvider(t, name, Numeric, "1e10000", "3e-1000")
		original := v
		for _, checkpoint := range []int{2, 3, 4, 5, 6, 7, 8} {
			ctx := &mathCheckpointContext{Context: context.Background(), remaining: checkpoint}
			got, err := p.Evaluate(ctx, v[0], v[1])
			if got != (Value{}) || !errors.Is(err, context.Canceled) || v != original {
				t.Fatal("large exact checkpoint cancellation", name, checkpoint, got, err)
			}
			var state interface{ SQLState() string }
			if errors.As(err, &state) {
				t.Fatal("checkpoint cancellation acquired SQLSTATE", err)
			}
		}
	}
}
