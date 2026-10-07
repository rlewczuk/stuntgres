package sqltype

import (
	"context"
	"errors"
	"strings"
	"testing"

	"stuntgres/internal/sqlstate"
)

func TestNumericRandomLattice(t *testing.T) {
	var d *Descriptor
	for _, typ := range Builtins() {
		if typ.ID() == Numeric {
			d = typ
		}
	}
	input := func(s string) Value {
		t.Helper()
		v, e := d.Input(context.Background(), s, -1)
		if e != nil {
			t.Fatal(e)
		}
		return v
	}
	for _, tc := range []struct {
		lo, hi string
		words  []uint64
		want   string
	}{
		{"-0.001", "0.001", []uint64{3, 0}, "-0.001"},
		{"-0.001", "0.001", []uint64{1}, "0.000"},
		{"-0.001", "0.001", []uint64{2}, "0.001"},
		{"1.0", "1.000", nil, "1.000"},
		{"0.00", "0.000", nil, "0.000"},
		{"100000000000000000000.000", "100000000000000000000.002", []uint64{1}, "100000000000000000000.001"},
		{"0.00000000000000000000", "0.00000000000000000002", []uint64{2}, "0.00000000000000000002"},
		{"-1", "18446744073709551614", []uint64{0}, "-1"},
		{"-1", "18446744073709551614", []uint64{^uint64(0)}, "18446744073709551614"},
		{"0", "18446744073709551616", []uint64{1, 0}, "18446744073709551616"},
	} {
		t.Run(tc.want, func(t *testing.T) {
			lo, hi := input(tc.lo), input(tc.hi)
			i := 0
			v, e := NumericRandom(context.Background(), lo, hi, func() uint64 {
				if i >= len(tc.words) {
					t.Fatal("unexpected draw")
				}
				w := tc.words[i]
				i++
				return w
			})
			if e != nil {
				t.Fatal(e)
			}
			text, e := d.Output(context.Background(), v)
			if e != nil || text != tc.want || i != len(tc.words) {
				t.Fatalf("output %q draws %d error %v", text, i, e)
			}
			if lo != input(tc.lo) || hi != input(tc.hi) {
				t.Fatal("mutated immutable operands")
			}
		})
	}
}

func TestNumericRandomExtremesAndCancellation(t *testing.T) {
	var d *Descriptor
	for _, typ := range Builtins() {
		if typ.ID() == Numeric {
			d = typ
		}
	}
	for _, s := range []string{"0." + strings.Repeat("0", 16382) + "1", "1" + strings.Repeat("0", 131071)} {
		v, e := d.Input(context.Background(), s, -1)
		if e != nil {
			t.Fatal(e)
		}
		out, e := NumericRandom(context.Background(), v, v, func() uint64 { t.Fatal("equal endpoint consumed state"); return 0 })
		if e != nil || out != v {
			t.Fatal("extreme equal endpoint", e)
		}
		text, e := d.Output(context.Background(), out)
		if e != nil || text != s {
			t.Fatal("extreme output changed", len(text), len(s), e)
		}
	}
	lo, _ := d.Input(context.Background(), "0", -1)
	hi, _ := d.Input(context.Background(), "2", -1)
	ctx, cancel := context.WithCancel(context.Background())
	calls := 0
	_, e := NumericRandom(ctx, lo, hi, func() uint64 { calls++; cancel(); return 3 })
	if !errors.Is(e, context.Canceled) || calls != 1 {
		t.Fatal("rejection cancellation", calls, e)
	}
}

func TestNumericRandomDiagnostics(t *testing.T) {
	var d *Descriptor
	for _, typ := range Builtins() {
		if typ.ID() == Numeric {
			d = typ
		}
	}
	for _, tc := range []struct{ lo, hi, msg string }{
		{"NaN", "Infinity", "lower bound cannot be NaN"},
		{"Infinity", "NaN", "lower bound cannot be infinity"},
		{"-Infinity", "0", "lower bound cannot be infinity"},
		{"0", "NaN", "upper bound cannot be NaN"},
		{"0", "-Infinity", "upper bound cannot be infinity"},
		{"2", "1", "lower bound must be less than or equal to upper bound"},
	} {
		lo, _ := d.Input(context.Background(), tc.lo, -1)
		hi, _ := d.Input(context.Background(), tc.hi, -1)
		_, e := NumericRandom(context.Background(), lo, hi, func() uint64 { t.Fatal("invalid bounds consumed state"); return 0 })
		var got *sqlstate.Error
		want := sqlstate.Error{Severity: "ERROR", SeverityUnlocalized: "ERROR", Code: "22023", Message: tc.msg}
		if !errors.As(e, &got) || *got != want {
			t.Fatalf("got %#v want %#v", got, want)
		}
	}
}

func TestNumericRandomVoidResult(t *testing.T) {
	var d *Descriptor
	for _, typ := range Builtins() {
		if typ.ID() == Void {
			d = typ
		}
	}
	v := VoidValue(d)
	if v.IsNull() || v.Type() != d || !Null(d).IsNull() || d.ID() != 2278 {
		t.Fatal("void identity")
	}
	text, e := d.Output(context.Background(), v)
	if e != nil || text != "" {
		t.Fatal(text, e)
	}
}

func TestNumericRandomNondegenerateExtremes(t *testing.T) {
	var d *Descriptor
	for _, typ := range Builtins() {
		if typ.ID() == Numeric {
			d = typ
		}
	}
	lo, e := d.Input(context.Background(), "0", -1)
	if e != nil {
		t.Fatal(e)
	}
	hi, e := d.Input(context.Background(), "1"+strings.Repeat("0", 131071), -1)
	if e != nil {
		t.Fatal(e)
	}
	v, e := NumericRandom(context.Background(), lo, hi, func() uint64 { return 0 })
	if e != nil {
		t.Fatal(e)
	}
	text, e := d.Output(context.Background(), v)
	if e != nil || text != "0" {
		t.Fatal("wide nondegenerate sample", len(text), e)
	}
	hi, e = d.Input(context.Background(), "0."+strings.Repeat("0", 16382)+"2", -1)
	if e != nil {
		t.Fatal(e)
	}
	v, e = NumericRandom(context.Background(), lo, hi, func() uint64 { return 1 })
	if e != nil {
		t.Fatal(e)
	}
	text, e = d.Output(context.Background(), v)
	if e != nil || len(text) != 16385 || !strings.HasSuffix(text, "1") || strings.Trim(text[2:len(text)-1], "0") != "" {
		t.Fatal("fine nondegenerate sample", len(text), e)
	}
}
