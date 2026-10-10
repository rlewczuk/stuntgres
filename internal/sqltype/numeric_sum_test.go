package sqltype

import (
	"context"
	"testing"
)

func TestNumericSumArithmetic(t *testing.T) {
	types := testTypes()
	for _, tc := range []struct {
		id     ID
		inputs []string
		want   string
	}{
		{Int2, []string{"32767", "32767"}, "65534"},
		{Int4, []string{"-2147483648", "-2147483648"}, "-4294967296"},
		{Int8, []string{"9223372036854775807", "9223372036854775807"}, "18446744073709551614"},
		{Int8, []string{"-9223372036854775808", "-9223372036854775808"}, "-18446744073709551616"},
		{Int8, []string{"9007199254740993", "1"}, "9007199254740994"},
		{Numeric, []string{"1.20", "3.456"}, "4.656"},
		{Numeric, []string{"1.20", "-1.20", "0.0000"}, "0.0000"},
		{Numeric, []string{"-1.20", "-3.456"}, "-4.656"},
		{Numeric, []string{"-1000", "0.0001"}, "-999.9999"},
		{Numeric, []string{"0.0000"}, "0.0000"},
	} {
		var s NumericSumState
		if err := s.Add(Null(types[tc.id])); err != nil {
			t.Fatal(err)
		}
		for _, text := range tc.inputs {
			if err := s.Add(numericAvgInput(t, types[tc.id], text)); err != nil {
				t.Fatal(err)
			}
		}
		sum, exponent, scale := s.sum.String(), s.exponent, s.scale
		var held Value
		for range 2 {
			v, err := s.Result(types[Numeric])
			if err != nil || v.IsNull() || v.Type() != types[Numeric] || numericOutput(v) != tc.want {
				t.Fatal(tc, numericOutput(v), err)
			}
			held = v
		}
		if s.sum.String() != sum || s.exponent != exponent || s.scale != scale {
			t.Fatal("Result changed exact state")
		}
		if err := s.Add(numericAvgInput(t, types[tc.id], "2")); err != nil {
			t.Fatal(err)
		}
		if numericOutput(held) != tc.want {
			t.Fatal("immutable result changed after Add")
		}
	}
}

func TestNumericSumBoundaries(t *testing.T) {
	d := testTypes()[Numeric]
	for _, tc := range []struct {
		inputs []string
		want   string
	}{
		{[]string{"NaN", "1"}, "NaN"},
		{[]string{"Infinity"}, "Infinity"},
		{[]string{"-Infinity", "1"}, "-Infinity"},
		{[]string{"Infinity", "-Infinity"}, "NaN"},
		{[]string{"9e131071", "9e131071", "Infinity"}, "Infinity"},
		{[]string{"9e131071", "9e131071", "NaN"}, "NaN"},
		{[]string{"9e131071", "9e131071", "-9e131071", "-9e131071"}, "0"},
	} {
		var s NumericSumState
		for _, text := range tc.inputs {
			if err := s.Add(numericAvgInput(t, d, text)); err != nil {
				t.Fatal(err)
			}
		}
		for range 2 {
			v, err := s.Result(d)
			if err != nil || v.IsNull() || numericOutput(v) != tc.want {
				t.Fatal(tc.inputs, err)
			}
		}
	}
	var s NumericSumState
	maximum := numericAvgInput(t, d, "9e131071")
	for range 2 {
		if err := s.Add(maximum); err != nil {
			t.Fatal("transient subtotal rejected", err)
		}
	}
	_, err := s.Result(d)
	numericAvgRange(t, err)
	if err := s.Add(numericAvgInput(t, d, "-9e131071")); err != nil {
		t.Fatal(err)
	}
	v, err := s.Result(d)
	if err != nil || v.text != "9" || v.exponent != 131071 {
		t.Fatal("failed packing poisoned state", err)
	}
}

func TestNumericSumEmptyAndAllocations(t *testing.T) {
	types := testTypes()
	var s NumericSumState
	if err := s.Add(Null(types[Numeric])); err != nil {
		t.Fatal(err)
	}
	v, err := s.Result(types[Numeric])
	if err != nil || !v.IsNull() || v.Type() != types[Numeric] {
		t.Fatal(v, err)
	}
	for _, invalid := range []Value{{}, Null(types[Float8])} {
		if s.Add(invalid) == nil {
			t.Fatal("invalid input accepted")
		}
	}
	if _, err := s.Result(types[Int8]); err == nil {
		t.Fatal("invalid descriptor accepted")
	}
	var absent *NumericSumState
	if absent.Add(Null(types[Numeric])) == nil {
		t.Fatal("nil state accepted")
	}
	if _, err := absent.Result(types[Numeric]); err == nil {
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
		t.Fatal("warmed exact integer allocations", n)
	}
	// NULL does not destroy an existing finite zero or its display scale.
	var zero NumericSumState
	if err := zero.Add(numericAvgInput(t, types[Numeric], "-0.0000")); err != nil {
		t.Fatal(err)
	}
	if err := zero.Add(Null(types[Numeric])); err != nil {
		t.Fatal(err)
	}
	v, err = zero.Result(types[Numeric])
	text, outputErr := types[Numeric].Output(context.Background(), v)
	if err != nil || outputErr != nil || v.negative || v.IsNull() || text != "0.0000" {
		t.Fatal(v, text, err, outputErr)
	}
}
