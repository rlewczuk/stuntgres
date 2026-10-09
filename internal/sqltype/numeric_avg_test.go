package sqltype

import (
	"context"
	"errors"
	"strings"
	"stuntgres/internal/sqlstate"
	"testing"
)

func numericAvgInput(t *testing.T, d *Descriptor, text string) Value {
	t.Helper()
	v, err := d.Input(context.Background(), text, -1)
	if err != nil {
		t.Fatal(err)
	}
	return v
}

func TestNumericAvgArithmetic(t *testing.T) {
	types := testTypes()
	for _, tc := range []struct {
		id     ID
		inputs []string
		want   string
	}{
		{Int2, []string{"1", "3"}, "2.0000000000000000"},
		{Int4, []string{"1"}, "1.00000000000000000000"},
		{Int8, []string{"9223372036854775807", "9223372036854775807"}, "9223372036854775807"},
		{Int8, []string{"9223372036854775807", "-9223372036854775808"}, "-0.50000000000000000000"},
		{Int8, []string{"9007199254740993", "9007199254740993"}, "9007199254740993.0000"},
		{Numeric, []string{"0", "1", "0"}, "0.33333333333333333333"},
		{Numeric, []string{"0", "-1", "0"}, "-0.33333333333333333333"},
		{Numeric, []string{"0"}, "0.00000000000000000000"},
		{Numeric, []string{"1.0000000000000000000000000", "3"}, "2.0000000000000000000000000"},
		{Numeric, []string{"1.0000000000000000000000000", "-1"}, "0.0000000000000000000000000"},
		{Numeric, []string{"0.0000000000000000000000000"}, "0.0000000000000000000000000"},
		{Numeric, []string{"0.1"}, "0.10000000000000000000"},
		{Numeric, []string{"0.0001"}, "0.000100000000000000000000"},
		{Numeric, []string{"0.00001"}, "0.000010000000000000000000"},
		{Numeric, []string{"10000"}, "10000.0000000000000000"},
		{Numeric, []string{"4e-1001"}, "0." + strings.Repeat("0", 1000)},
		{Numeric, []string{"5e-1001"}, "0." + strings.Repeat("0", 999) + "1"},
		{Numeric, []string{"-5e-1001"}, "-0." + strings.Repeat("0", 999) + "1"},
		{Numeric, []string{"9995e-1001"}, "0." + strings.Repeat("0", 996) + "1000"},
		{Numeric, []string{"0e-1001"}, "0." + strings.Repeat("0", 1000)},
	} {
		t.Run(tc.want[:min(len(tc.want), 40)], func(t *testing.T) {
			var s NumericAvgState
			if err := s.Add(Null(types[tc.id])); err != nil {
				t.Fatal(err)
			}
			for _, text := range tc.inputs {
				if err := s.Add(numericAvgInput(t, types[tc.id], text)); err != nil {
					t.Fatal(err)
				}
			}
			sum, count, exponent := s.sum.String(), s.count.String(), s.exponent
			var held Value
			for range 2 {
				v, err := s.Result(types[Numeric])
				if err != nil || v.Type() != types[Numeric] || numericOutput(v) != tc.want {
					t.Fatalf("mean mismatch: %v", err)
				}
				held = v
			}
			if s.sum.String() != sum || s.count.String() != count || s.exponent != exponent {
				t.Fatal("Result changed mathematical state")
			}
			if err := s.Add(numericAvgInput(t, types[tc.id], "2")); err != nil {
				t.Fatal(err)
			}
			if numericOutput(held) != tc.want {
				t.Fatal("escaped result aliases mutable state")
			}
		})
	}
}

func numericAvgRange(t *testing.T, err error) {
	t.Helper()
	var d *sqlstate.Error
	if !errors.As(err, &d) || d.Code != "22003" || d.Message != "value overflows numeric format" || d.Position != "" || d.Detail != "" || d.Hint != "" || d.Context != "" {
		t.Fatalf("range diagnostic: %#v", err)
	}
}

func TestNumericAvgBoundaries(t *testing.T) {
	d := testTypes()[Numeric]
	for _, tc := range []struct {
		input []string
		want  string
	}{
		{[]string{"NaN", "1"}, "NaN"},
		{[]string{"Infinity", "Infinity"}, "Infinity"},
		{[]string{"-Infinity", "1"}, "-Infinity"},
		{[]string{"Infinity", "-Infinity"}, "NaN"},
		{[]string{"9e131071", "9e131071", "Infinity"}, "Infinity"},
		{[]string{"9e131071", "9e131071", "NaN"}, "NaN"},
		{[]string{"9e131071", "9e131071", "-9e131071", "-9e131071"}, "0.00000000000000000000"},
	} {
		var s NumericAvgState
		for _, text := range tc.input {
			if err := s.Add(numericAvgInput(t, d, text)); err != nil {
				t.Fatal(err)
			}
		}
		v, err := s.Result(d)
		if err != nil || numericOutput(v) != tc.want {
			t.Fatal(tc.input, err)
		}
	}
	var s NumericAvgState
	maximum := numericAvgInput(t, d, "9e131071")
	if err := s.Add(maximum); err != nil {
		t.Fatal(err)
	}
	v, err := s.Result(d)
	if err != nil || v.text != "9" || v.exponent != 131071 || v.scale != 0 {
		t.Fatal("maximum singleton", err)
	}
	if err := s.Add(maximum); err != nil {
		t.Fatal("transient sum rejected", err)
	}
	_, err = s.Result(d)
	numericAvgRange(t, err)
	if err := s.Add(numericAvgInput(t, d, "-9e131071")); err != nil {
		t.Fatal(err)
	}
	if _, err := s.Result(d); err != nil {
		t.Fatal("finalization poisoned state", err)
	}
}

func TestNumericAvgOwnershipAndIntegerAllocations(t *testing.T) {
	types := testTypes()
	var s NumericAvgState
	v, err := s.Result(types[Numeric])
	if err != nil || !v.IsNull() || v.Type() != types[Numeric] {
		t.Fatal(v, err)
	}
	for _, invalid := range []Value{{}, Null(types[Float8])} {
		if err := s.Add(invalid); err == nil {
			t.Fatal("invalid datum accepted")
		}
	}
	if _, err := s.Result(types[Int4]); err == nil {
		t.Fatal("invalid result descriptor accepted")
	}
	var nilState *NumericAvgState
	if err := nilState.Add(Null(types[Numeric])); err == nil {
		t.Fatal("nil state accepted")
	}
	if _, err := nilState.Result(types[Numeric]); err == nil {
		t.Fatal("nil result state accepted")
	}
	one, _ := Integer(types[Int8], 1)
	for range 100 {
		if err := s.Add(one); err != nil {
			t.Fatal(err)
		}
	}
	if n := testing.AllocsPerRun(100, func() {
		if err := s.Add(one); err != nil {
			panic(err)
		}
	}); n != 0 {
		t.Fatalf("warmed integer transition allocations: %g", n)
	}
	// A count is not narrowed to int64 and cannot silently wrap.
	s.count.SetString("9223372036854775807", 10)
	if err := s.Add(one); err != nil || s.count.String() != "9223372036854775808" {
		t.Fatal("count wrapped", err)
	}
}
