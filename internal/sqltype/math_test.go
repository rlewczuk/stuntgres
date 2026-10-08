package sqltype

import (
	"context"
	"errors"
	"math"
	"strings"
	"testing"

	"stuntgres/internal/sqlstate"
)

func mathTypes() map[ID]*Descriptor {
	m := make(map[ID]*Descriptor)
	for _, d := range Builtins() {
		m[d.ID()] = d
	}
	return m
}

func TestMathSignatures(t *testing.T) {
	types := mathTypes()
	for _, tc := range []struct {
		name   string
		args   []ID
		result ID
	}{
		{"scale", []ID{Numeric}, Int4}, {"min_scale", []ID{Numeric}, Int4}, {"trim_scale", []ID{Numeric}, Numeric},
		{"round", []ID{Numeric}, Numeric}, {"round", []ID{Float8}, Float8}, {"round", []ID{Numeric, Int4}, Numeric},
		{"trunc", []ID{Numeric}, Numeric}, {"trunc", []ID{Float8}, Float8}, {"trunc", []ID{Numeric, Int4}, Numeric},
		{"sign", []ID{Numeric}, Numeric}, {"sign", []ID{Float8}, Float8},
		{"mod", []ID{Int2, Int2}, Int2}, {"mod", []ID{Int4, Int4}, Int4}, {"mod", []ID{Int8, Int8}, Int8}, {"mod", []ID{Numeric, Numeric}, Numeric},
	} {
		var usages []Usage
		for _, id := range tc.args {
			usages = append(usages, types[id].Usage())
		}
		p, err := BindMath(tc.name, types[tc.result].Usage(), usages...)
		if err != nil || p.ResultUsage() != types[tc.result].Usage() {
			t.Fatal(tc, err)
		}
		for i := range usages {
			var v [2]Value
			for j, u := range usages {
				v[j], err = u.Type.Input(context.Background(), "1", -1)
				if err != nil {
					t.Fatal(err)
				}
			}
			v[i] = Null(usages[i].Type)
			got, err := p.Evaluate(context.Background(), v[0], v[1])
			if err != nil || got != Null(types[tc.result]) {
				t.Fatal("typed strict NULL", tc, got, err)
			}
		}
	}
	for _, tc := range []struct {
		name   string
		args   []ID
		result ID
	}{
		{"round", []ID{Int4}, Int4}, {"round", []ID{Float8, Int4}, Float8}, {"round", []ID{Numeric, Int8}, Numeric},
		{"scale", []ID{Float8}, Int4}, {"mod", []ID{Float8, Float8}, Float8},
		{"sign", []ID{Numeric}, Int4},
	} {
		var usages []Usage
		for _, id := range tc.args {
			usages = append(usages, types[id].Usage())
		}
		if _, err := BindMath(tc.name, types[tc.result].Usage(), usages...); err == nil {
			t.Fatal("invalid signature", tc)
		}
	}
}

func TestMathNumericRoundScale(t *testing.T) {
	types := mathTypes()
	ctx := context.Background()
	for _, tc := range []struct{ name, input, scale, want string }{
		{"round", "2.5", "", "3"}, {"round", "-2.5", "", "-3"},
		{"round", "-1.25", "1", "-1.3"}, {"trunc", "-1.25", "1", "-1.2"},
		{"round", "9.995", "2", "10.00"}, {"round", "-9.995", "2", "-10.00"},
		{"round", "1500", "-3", "2000"}, {"round", "-1500", "-3", "-2000"},
		{"trunc", "1500", "-3", "1000"}, {"trunc", "-1500", "-3", "-1000"},
		{"round", "1.23", "4", "1.2300"}, {"trunc", "1.23", "4", "1.2300"},
		{"round", "1.2300", "", "1"}, {"trunc", "1.2300", "", "1"},
		{"scale", "8.4100", "", "4"}, {"min_scale", "8.4100", "", "2"}, {"trim_scale", "8.4100", "", "8.41"},
		{"scale", "0.0000", "", "4"}, {"min_scale", "0.0000", "", "0"}, {"trim_scale", "0.0000", "", "0"},
		{"min_scale", "-13.000", "", "0"}, {"trim_scale", "-13.000", "", "-13"},
		{"scale", "1e-10", "", "10"}, {"min_scale", "1e-10", "", "10"}, {"trim_scale", "1e-10", "", "0.0000000001"},
		{"round", "5.5e131071", "-131073", "0"}, {"round", "5.5e131071", "-1000000", "0"}, {"trunc", "5.5e131071", "-131072", "0"},
		{"round", "1.25", "-2147483648", "0"}, {"trunc", "1.25", "-2147483648", "0"},
	} {
		a, err := types[Numeric].Input(ctx, tc.input, -1)
		if err != nil {
			t.Fatal(err)
		}
		original := a
		usages := []Usage{types[Numeric].Usage()}
		var b Value
		if tc.scale != "" {
			usages = append(usages, types[Int4].Usage())
			b, err = types[Int4].Input(ctx, tc.scale, -1)
			if err != nil {
				t.Fatal(err)
			}
		}
		result := types[Numeric]
		if tc.name == "scale" || tc.name == "min_scale" {
			result = types[Int4]
		}
		p, err := BindMath(tc.name, result.Usage(), usages...)
		if err != nil {
			t.Fatal(err)
		}
		v, err := p.Evaluate(ctx, a, b)
		if err != nil {
			t.Fatal(tc, err)
		}
		text, err := result.Output(ctx, v)
		if err != nil || text != tc.want || a != original {
			t.Fatalf("%v: %q %v", tc, text, err)
		}
	}
	for _, name := range []string{"round", "trunc"} {
		p, _ := BindMath(name, types[Numeric].Usage(), types[Numeric].Usage(), types[Int4].Usage())
		a, _ := types[Numeric].Input(ctx, "1.23", -1)
		b, _ := types[Int4].Input(ctx, "2147483647", -1)
		v, err := p.Evaluate(ctx, a, b)
		text, e := types[Numeric].Output(ctx, v)
		if err != nil || e != nil || text != "1.23"+strings.Repeat("0", 16381) {
			t.Fatal("scale clamp", name, len(text), err, e)
		}
	}
	p, _ := BindMath("round", types[Numeric].Usage(), types[Numeric].Usage(), types[Int4].Usage())
	a, _ := types[Numeric].Input(ctx, "5.5e131071", -1)
	b, _ := types[Int4].Input(ctx, "-131072", -1)
	_, err := p.Evaluate(ctx, a, b)
	var diagnostic *sqlstate.Error
	if !errors.As(err, &diagnostic) || diagnostic.Code != "22003" || diagnostic.Message != "value overflows numeric format" {
		t.Fatal(err)
	}
	for _, special := range []string{"NaN", "Infinity", "-Infinity"} {
		a, _ := types[Numeric].Input(ctx, special, -1)
		for _, name := range []string{"round", "trunc", "trim_scale", "scale", "min_scale"} {
			d := types[Numeric]
			if name == "scale" || name == "min_scale" {
				d = types[Int4]
			}
			p, _ := BindMath(name, d.Usage(), types[Numeric].Usage())
			v, err := p.Evaluate(ctx, a, Value{})
			if err != nil || (d.id == Int4 && !v.null) || (d.id == Numeric && v != a) {
				t.Fatal(name, special, v, err)
			}
		}
	}
}

func TestMathSignModulo(t *testing.T) {
	types := mathTypes()
	ctx := context.Background()
	for _, tc := range []struct{ name, input, right, want string }{
		{"sign", "-13.000", "", "-1"}, {"sign", "0.0000", "", "0"}, {"sign", "13.000", "", "1"},
		{"sign", "NaN", "", "NaN"}, {"sign", "Infinity", "", "1"}, {"sign", "-Infinity", "", "-1"},
		{"mod", "-9.00", "4.0", "-1.00"}, {"mod", "9.00", "-4.000", "1.000"}, {"mod", "-9.00", "-4.000", "-1.000"},
		{"mod", "8.00", "4.000", "0.000"}, {"mod", "0.0000", "4.0", "0.0000"},
		{"mod", "1.2300", "1000.000000", "1.230000"}, {"mod", "9007199254740993.25", "2.0", "1.25"},
		{"mod", "-9007199254740993.25", "2.0", "-1.25"}, {"mod", "9223372036854775808", "3", "2"},
		{"mod", "3e-30", "2e-30", "0." + strings.Repeat("0", 29) + "1"},
		{"mod", "NaN", "0", "NaN"}, {"mod", "0", "NaN", "NaN"}, {"mod", "Infinity", "1", "NaN"},
		{"mod", "-Infinity", "Infinity", "NaN"}, {"mod", "1.2300", "Infinity", "1.2300"}, {"mod", "1.2300", "-Infinity", "1.2300"},
	} {
		a, err := types[Numeric].Input(ctx, tc.input, -1)
		if err != nil {
			t.Fatal(err)
		}
		usages := []Usage{types[Numeric].Usage()}
		var b Value
		if tc.right != "" {
			usages = append(usages, types[Numeric].Usage())
			b, err = types[Numeric].Input(ctx, tc.right, -1)
			if err != nil {
				t.Fatal(err)
			}
		}
		p, err := BindMath(tc.name, types[Numeric].Usage(), usages...)
		if err != nil {
			t.Fatal(err)
		}
		originalA, originalB := a, b
		v, err := p.Evaluate(ctx, a, b)
		if err != nil {
			t.Fatal(tc, err)
		}
		text, err := types[Numeric].Output(ctx, v)
		if err != nil || text != tc.want || a != originalA || b != originalB {
			t.Fatal(tc, text, err)
		}
	}
	for _, id := range []ID{Int2, Int4, Int8} {
		p, _ := BindMath("mod", types[id].Usage(), types[id].Usage(), types[id].Usage())
		minimum := int64(math.MinInt64)
		if id == Int2 {
			minimum = math.MinInt16
		}
		if id == Int4 {
			minimum = math.MinInt32
		}
		for _, tc := range [][3]int64{{9, 4, 1}, {-9, 4, -1}, {9, -4, 1}, {-9, -4, -1}, {minimum, -1, 0}} {
			a, _ := Integer(types[id], tc[0])
			b, _ := Integer(types[id], tc[1])
			got, err := p.Evaluate(ctx, a, b)
			if err != nil || got.integer != tc[2] {
				t.Fatal(id, tc, got, err)
			}
		}
	}
	for _, id := range []ID{Int2, Int4, Int8, Numeric} {
		p, _ := BindMath("mod", types[id].Usage(), types[id].Usage(), types[id].Usage())
		a, _ := types[id].Input(ctx, "1", -1)
		b, _ := types[id].Input(ctx, "0", -1)
		_, err := p.Evaluate(ctx, a, b)
		var got *sqlstate.Error
		want := sqlstate.Error{Severity: "ERROR", SeverityUnlocalized: "ERROR", Code: "22012", Message: "division by zero"}
		if !errors.As(err, &got) || *got != want {
			t.Fatal(id, got, err)
		}
	}
	for _, name := range []string{"round", "trunc", "sign"} {
		p, _ := BindMath(name, types[Float8].Usage(), types[Float8].Usage())
		for _, n := range []float64{-3.5, -2.5, -0.5, math.Copysign(0, -1), 0, 2.5, 3.5, math.Inf(-1), math.Inf(1), math.NaN()} {
			a, _ := Float(types[Float8], n)
			got, err := p.Evaluate(ctx, a, Value{})
			want := math.RoundToEven(n)
			if name == "trunc" {
				want = math.Trunc(n)
			}
			if name == "sign" {
				want = 0
				if n > 0 {
					want = 1
				}
				if n < 0 {
					want = -1
				}
			}
			if err != nil || (math.IsNaN(want) && !math.IsNaN(got.floating)) || (!math.IsNaN(want) && (got.floating != want || math.Signbit(got.floating) != math.Signbit(want))) {
				t.Fatal(name, n, got, err)
			}
		}
	}
}

func TestMathCancellation(t *testing.T) {
	types := mathTypes()
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	p, _ := BindMath("mod", types[Numeric].Usage(), types[Numeric].Usage(), types[Numeric].Usage())
	a, _ := types[Numeric].Input(context.Background(), "1e131071", -1)
	b, _ := types[Numeric].Input(context.Background(), "3e-16383", -1)
	_, err := p.Evaluate(ctx, a, b)
	if !errors.Is(err, context.Canceled) {
		t.Fatal(err)
	}
	for _, checkpoint := range []int{2, 3, 4} {
		ctx := &mathCheckpointContext{Context: context.Background(), remaining: checkpoint}
		originalA, originalB := a, b
		_, err := p.Evaluate(ctx, a, b)
		if !errors.Is(err, context.Canceled) || a != originalA || b != originalB {
			t.Fatal("large exact work cancellation checkpoint", checkpoint, err)
		}
	}
}

// Cancel at an exact provider checkpoint, without sleeps or a racing goroutine.
type mathCheckpointContext struct {
	context.Context
	remaining int
}

func (c *mathCheckpointContext) Err() error {
	c.remaining--
	if c.remaining <= 0 {
		return context.Canceled
	}
	return nil
}
