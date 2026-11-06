package dual

import (
	"database/sql/driver"
	"math"
	"testing"
	"time"
)

func TestDualNormalizeValues(t *testing.T) {
	plus2 := time.FixedZone("", 2*3600)
	instant := time.Date(2024, 1, 2, 5, 4, 5, 120_000_000, plus2)
	for _, tc := range []struct {
		typ   string
		value driver.Value
		want  string // "<nil>" is SQL NULL
	}{
		{"TEXT", nil, "<nil>"},
		{"BYTEA", []byte{0, 0xff}, `\x00ff`},
		{"TEXT", []byte("x"), "x"},
		{"INT4", int64(-5), "-5"},
		{"OID", []byte("16384"), "16384"},
		{"OID", int64(16384), "16384"},
		{"BOOL", true, "t"},
		{"BOOL", []byte("f"), "f"},
		{"FLOAT4", float64(float32(0.1)), "0.1"},
		{"FLOAT4", 0.1, "0.1"},
		{"FLOAT8", 0.1, "0.1"},
		{"FLOAT8", math.NaN(), "NaN"},
		{"FLOAT8", math.Inf(-1), "-Infinity"},
		{"FLOAT8", "1.5", "1.5"},
		{"TIMESTAMPTZ", instant, "2024-01-02 03:04:05.12+00"},
		{"TIMESTAMPTZ", instant.UTC(), "2024-01-02 03:04:05.12+00"},
		{"TIMESTAMPTZ", instant.Local(), "2024-01-02 03:04:05.12+00"},
		{"TIMESTAMP", time.Date(2024, 1, 2, 3, 4, 5, 123_456_000, time.FixedZone("", 0)), "2024-01-02 03:04:05.123456"},
		{"DATE", time.Date(2024, 2, 29, 0, 0, 0, 0, time.UTC), "2024-02-29"},
		{"DATE", "2024-02-29", "2024-02-29"},
		{"DATE", time.Date(-43, 3, 15, 0, 0, 0, 0, time.UTC), "0044-03-15 BC"},
		{"TIME", time.Date(0, 1, 1, 12, 34, 56, 789_000_000, time.UTC), "12:34:56.789"},
		{"TIME", time.Date(0, 1, 2, 0, 0, 0, 0, time.UTC), "24:00:00"},
		{"TIMETZ", time.Date(0, 1, 1, 12, 34, 56, 0, time.FixedZone("", 5*3600+30*60)), "12:34:56+05:30"},
		{"TIMETZ", time.Date(0, 1, 1, 12, 34, 56, 0, time.FixedZone("", -8*3600)), "12:34:56-08"},
		{"TIMESTAMP", []byte("infinity"), "infinity"},
		{"TIMESTAMP", "infinity", "infinity"},
		{"NUMERIC", []byte("1.50"), "1.50"},
		{"NUMERIC", "1.50", "1.50"},
	} {
		got := "<nil>"
		if s := canonical(tc.typ, tc.value); s != nil {
			got = *s
		}
		if got != tc.want {
			t.Errorf("canonical(%s, %#v) = %q, want %q", tc.typ, tc.value, got, tc.want)
		}
	}
}

func TestDualNormalizeHook(t *testing.T) {
	var seen []Value
	o := &Options{Normalize: func(v Value) (string, bool) {
		seen = append(seen, v)
		if v.Value == "override" {
			return "hooked", true
		}
		return "", false
	}}
	if got := normalize(o, true, "c", "TEXT", "override"); got == nil || *got != "hooked" {
		t.Fatalf("override: %v", got)
	}
	if got := normalize(o, false, "b", "BOOL", true); got == nil || *got != "t" {
		t.Fatalf("fallback: %v", got)
	}
	if got := normalize(o, false, "n", "TEXT", nil); got != nil {
		t.Fatalf("NULL: %q", *got)
	}
	want := []Value{{Reference: true, Column: "c", Type: "TEXT", Value: "override"}, {Column: "b", Type: "BOOL", Value: true}}
	if len(seen) != len(want) || seen[0] != want[0] || seen[1] != want[1] {
		t.Fatalf("hook saw %+v, want %+v", seen, want)
	}
}
