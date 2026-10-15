package sqltype

import (
	"context"
	"errors"
	"testing"
)

func TestSizeProviderSignatures(t *testing.T) {
	types := mathTypes()
	for _, name := range []string{"pg_size_bytes", "pg_size_pretty"} {
		for id, arg := range types {
			for rid, result := range types {
				valid := name == "pg_size_bytes" && id == Text && rid == Int8 || name == "pg_size_pretty" && (id == Int8 || id == Numeric) && rid == Text
				p, err := BindMath(name, result.Usage(), arg.Usage())
				if (err == nil) != valid {
					t.Fatal(name, id, rid, err)
				}
				if !valid {
					continue
				}
				if p.ResultUsage() != result.Usage() {
					t.Fatal("result owner", p)
				}
				v, err := p.Evaluate(context.Background(), []Value{Null(arg)}, InputEnvironment{})
				if err != nil || v != Null(result) {
					t.Fatal("strict NULL", v, err)
				}
				foreign := *arg
				if _, err := p.Evaluate(context.Background(), []Value{Null(&foreign)}, InputEnvironment{}); err == nil {
					t.Fatal("accepted foreign value owner")
				}
				if _, err := BindMath(name, result.Usage(), arg.Usage(), arg.Usage()); err == nil {
					t.Fatal("accepted binary signature")
				}
				if _, err := BindMath(name, result.Usage()); err == nil {
					t.Fatal("accepted missing argument")
				}
				constrained := arg.Usage()
				constrained.Typmod = 8
				if _, err := BindMath(name, result.Usage(), constrained); err == nil {
					t.Fatal("accepted constrained formal argument")
				}
				constrained = result.Usage()
				constrained.Typmod = 8
				if _, err := BindMath(name, constrained, arg.Usage()); err == nil {
					t.Fatal("accepted constrained result")
				}
			}
		}
	}
}

func TestSizeProviderOwnershipAndCancellation(t *testing.T) {
	types := mathTypes()
	for _, tc := range []struct {
		name        string
		arg, result ID
		input, want string
	}{
		{"pg_size_bytes", Text, Int8, ".00048828125kB", "1"},
		{"pg_size_bytes", Text, Int8, "-8192PB", "-9223372036854775808"},
		{"pg_size_pretty", Int8, Text, "-9223372036854775808", "-8192 PB"},
		{"pg_size_pretty", Numeric, Text, "10.5000", "10.5000 bytes"},
		{"pg_size_pretty", Numeric, Text, "11528652096115048448", "10240 PB"},
		{"pg_size_pretty", Numeric, Text, "NaN", "NaN PB"},
		{"pg_size_pretty", Numeric, Text, "-Infinity", "-Infinity PB"},
	} {
		p, err := BindMath(tc.name, types[tc.result].Usage(), types[tc.arg].Usage())
		if err != nil {
			t.Fatal(err)
		}
		a, err := types[tc.arg].Input(context.Background(), tc.input, -1)
		if err != nil {
			t.Fatal(err)
		}
		original := a
		for range 2 {
			got, err := p.Evaluate(context.Background(), []Value{a}, InputEnvironment{})
			if err != nil {
				t.Fatal(tc, err)
			}
			text, err := types[tc.result].Output(context.Background(), got)
			if err != nil || text != tc.want || a != original || got.Type() != types[tc.result] {
				t.Fatal(tc, text, err)
			}
		}
		ctx, cancel := context.WithCancel(context.Background())
		cancel()
		if _, err := p.Evaluate(ctx, []Value{a}, InputEnvironment{}); !errors.Is(err, context.Canceled) {
			t.Fatal("canceled provider", err)
		}
		if _, err := p.Evaluate(ctx, []Value{Null(types[tc.arg])}, InputEnvironment{}); !errors.Is(err, context.Canceled) {
			t.Fatal("canceled NULL provider", err)
		}
	}
}

func TestSizeNumericScratchAndScanner(t *testing.T) {
	v, err := parseNumeric("10.5000")
	if err != nil || v.Type() != nil || numericOutput(v) != "10.5000" {
		t.Fatal("private numeric scratch", v, err)
	}
	types := mathTypes()
	p, err := BindMath("pg_size_bytes", types[Int8].Usage(), types[Text].Usage())
	if err != nil {
		t.Fatal(err)
	}
	for _, input := range []string{"1 KB", "1 KiB", "1_000kB", "0x10", "1e", "1e+"} {
		_, err := p.Evaluate(context.Background(), []Value{String(types[Text], input)}, InputEnvironment{})
		var state interface{ SQLState() string }
		if !errors.As(err, &state) || state.SQLState() != "22023" {
			t.Fatal(input, err)
		}
	}
	// Constrained input still sizes before unconstrained format validation.
	v, err = types[Numeric].Input(context.Background(), "1e-20000", (3<<16)|4)
	if err != nil || v.Type() != types[Numeric] || numericOutput(v) != "0" {
		t.Fatal("preserved input ordering", v, err)
	}
}
