package settings

import (
	"math"
	"testing"
)

func TestParseAndFormatTypedInput(t *testing.T) {
	for _, tc := range []struct {
		d           Definition
		input, want string
	}{
		{Definition{Type: Boolean}, "TRUE", "on"}, {Definition{Type: Boolean}, "n", "off"},
		{Definition{Type: Integer, IntegerMin: -10, IntegerMax: 10}, "2.5", "2"}, {Definition{Type: Integer, IntegerMin: -10, IntegerMax: 10}, "3.5", "4"}, {Definition{Type: Integer, IntegerMin: -10, IntegerMax: 10}, "010", "8"}, {Definition{Type: Integer, IntegerMin: -10, IntegerMax: 10}, "0xA", "10"},
		{Definition{Type: Floating, FloatMax: 10}, "2.1234567", "2.12346"},
		{Definition{Type: Enum, EnumValues: []string{"safe", "fast"}}, "FAST", "fast"},
		{Definition{Type: Integer, Unit: Kilobytes, IntegerMax: math.MaxInt32}, "30.1GB", "30822MB"},
		{Definition{Type: Integer, Unit: Kilobytes, IntegerMax: math.MaxInt32}, "0.5kB", "0"},
		{Definition{Type: Integer, Unit: Bytes, IntegerMax: math.MaxInt32}, "0.5kB", "512B"},
		{Definition{Type: Integer, Unit: Milliseconds, IntegerMax: math.MaxInt32}, "1.5s", "1500ms"},
		{Definition{Type: Floating, Unit: Seconds, FloatMax: 10}, "0.001s", "1ms"},
		{Definition{Type: Floating, Unit: Seconds, FloatMax: 10}, "0.0000001", "0.1us"},
		{Definition{Type: Floating, Unit: Seconds, FloatMax: 10}, "0.0000001s", "0"},
		{Definition{Type: Floating, FloatMax: 10}, "0x1.8p0", "1.5"},
		{Definition{Type: Integer, IntegerMin: -10, IntegerMax: 10}, "-0.5", "0"},
	} {
		tc.d.Name = "demo.value"
		v, err := tc.d.Parse(tc.input)
		if err != nil || tc.d.Format(v) != tc.want {
			t.Errorf("%+v %q got %q err %v want %q", tc.d, tc.input, tc.d.Format(v), err, tc.want)
		}
	}
	for _, input := range []string{"0b10", "0o10", "1_000", "08", "NaN", "1kB", "1 2", "1e999"} {
		d := Definition{Name: "demo.count", Type: Integer, IntegerMax: 10}
		if _, err := d.Parse(input); err == nil {
			t.Errorf("accepted %q", input)
		}
	}
	for _, input := range []string{"o", " true ", ""} {
		if _, err := (Definition{Name: "demo.flag", Type: Boolean}).Parse(input); err == nil {
			t.Errorf("accepted Boolean %q", input)
		}
	}
	for _, input := range []string{"NaN", "1e999", "1e-999", "1e-320", "1_000", "0b10"} {
		if _, err := (Definition{Name: "demo.real", Type: Floating, FloatMax: 10}).Parse(input); err == nil {
			t.Errorf("accepted real %q", input)
		}
	}
}
