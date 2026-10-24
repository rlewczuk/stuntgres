package sqltype

import (
	"context"
	"errors"
	"fmt"
	"testing"
	"unsafe"

	"stuntgres/internal/sqlstate"
)

func bitDescriptors() (*Descriptor, *Descriptor) {
	var bit, varying *Descriptor
	for _, d := range Builtins() {
		switch d.ID() {
		case Bit:
			bit = d
		case VarBit:
			varying = d
		}
	}
	return bit, varying
}

func bitDiagnostic(t *testing.T, err error, code, message string) {
	t.Helper()
	want := sqlstate.Error{Severity: "ERROR", SeverityUnlocalized: "ERROR", Code: code, Message: message}
	var got *sqlstate.Error
	if !errors.As(err, &got) || *got != want {
		t.Fatalf("diagnostic %#v want %#v (%v)", got, want, err)
	}
}

func TestBitstringCodecAndOwnership(t *testing.T) {
	ctx := context.Background()
	bit, varying := bitDescriptors()
	for _, d := range []*Descriptor{bit, varying} {
		if d == nil || d.Usage().Collation != (CollationRef{}) || d.Usage().Typmod != -1 || IsCharacter(d.ID()) {
			t.Fatal(d)
		}
		if _, err := Array(d.ID()+1, "_"+d.Name(), d); err == nil {
			t.Fatal("bit array descriptor admitted")
		}
		array := testTypes()[d.AssociatedArray()]
		if _, err := ArrayValue(array, nil, nil); err == nil {
			t.Fatal("lookup-only bit array admitted an empty value")
		}
		if _, err := array.Input(ctx, "{}", -1); err == nil {
			t.Fatal("lookup-only bit array admitted textual input")
		}
		for _, tc := range []struct{ in, want string }{{"", ""}, {"B", ""}, {"X", ""}, {"0001", "0001"}, {"b1000", "1000"}, {"x0f", "00001111"}, {"XA", "1010"}, {"x0fA", "000011111010"}} {
			v, err := d.Input(ctx, tc.in, -1)
			if err != nil {
				t.Fatal(tc, err)
			}
			text, err := v.Bits()
			if err != nil || text != tc.want {
				t.Fatal(text, err, tc)
			}
			out, err := d.Output(ctx, v)
			if err != nil || out != tc.want {
				t.Fatal(out, err)
			}
			literal, err := d.SQLLiteral(ctx, v)
			if err != nil || literal != "B'"+tc.want+"'" {
				t.Fatal(literal, err)
			}
			copy, err := BitString(d, tc.want)
			if err != nil || !SameDatum(v, copy) {
				t.Fatal(v, copy, err)
			}
		}
		// Every small binary string, and all individual hex digits, independently
		// exercise width preservation without deriving expectations from output.
		for width := range 9 {
			for n := range 1 << width {
				text := ""
				if width > 0 {
					text = fmt.Sprintf("%0*b", width, n)
				}
				v, err := d.Input(ctx, text, -1)
				if err != nil || v.text != text {
					t.Fatal(width, n, v, err)
				}
				if len(text) > 0 && unsafe.StringData(v.text) != unsafe.StringData(text) {
					t.Fatal("binary input copied immutable payload")
				}
			}
		}
		for n := range 16 {
			v, err := d.Input(ctx, fmt.Sprintf("x%x", n), -1)
			if err != nil || v.text != fmt.Sprintf("%04b", n) {
				t.Fatal(n, v, err)
			}
		}
		for _, tc := range []struct{ in, digit, family string }{{"2", "2", "binary"}, {" 01", " ", "binary"}, {"01 ", " ", "binary"}, {"xG", "G", "hexadecimal"}, {"é", "é", "binary"}, {"x界", "界", "hexadecimal"}, {"😀", "😀", "binary"}} {
			_, err := d.Input(ctx, tc.in, -1)
			bitDiagnostic(t, err, "22P02", fmt.Sprintf("\"%s\" is not a valid %s digit", tc.digit, tc.family))
		}
		if s, err := d.SQLLiteral(ctx, Null(d)); err != nil || s != "NULL" {
			t.Fatal(s, err)
		}
		if _, err := d.Input(ctx, "1", 0); err == nil {
			t.Fatal("invalid typmod")
		}
	}
	if _, err := BitString(nil, "01"); err == nil {
		t.Fatal("nil constructor")
	}
	if _, err := BitString(testTypes()[Bool], "01"); err == nil {
		t.Fatal("wrong descriptor")
	}
	if _, err := BitString(bit, "02"); err == nil {
		t.Fatal("unchecked constructor")
	}
	for _, v := range []Value{{}, Null(bit), Boolean(testTypes()[Bool], true)} {
		if _, err := v.Bits(); err == nil {
			t.Fatal("invalid accessor")
		}
	}
	v, _ := BitString(bit, "001")
	foreign, _ := bitDescriptors()
	if _, err := foreign.Output(ctx, v); err == nil {
		t.Fatal("foreign output")
	}
	if _, err := foreign.SQLLiteral(ctx, v); err == nil {
		t.Fatal("foreign literal")
	}
	canceled, cancel := context.WithCancel(ctx)
	cancel()
	if _, err := bit.Input(canceled, "001", -1); !errors.Is(err, context.Canceled) {
		t.Fatal(err)
	}
	if _, err := bit.Output(canceled, v); !errors.Is(err, context.Canceled) {
		t.Fatal(err)
	}
	if _, err := bit.SQLLiteral(canceled, v); !errors.Is(err, context.Canceled) {
		t.Fatal(err)
	}
}

func TestBitstringModifiersAndAssignment(t *testing.T) {
	ctx := context.Background()
	bit, varying := bitDescriptors()
	for _, d := range []*Descriptor{bit, varying} {
		for _, n := range []int32{1, maximumBitLength} {
			u, warning, err := d.ResolveModifiers(n)
			if err != nil || warning != nil || u.Typmod != n || !u.Valid() {
				t.Fatal(u, warning, err)
			}
			if length, ok := u.Length(); !ok || length != int64(n) {
				t.Fatal(length, ok)
			}
		}
		for _, tc := range []struct {
			mods    []int32
			message string
		}{{nil, "invalid type modifier"}, {[]int32{1, 2}, "invalid type modifier"}, {[]int32{0}, "length for type " + d.Name() + " must be at least 1"}, {[]int32{-1}, "length for type " + d.Name() + " must be at least 1"}, {[]int32{maximumBitLength + 1}, "length for type " + d.Name() + " cannot exceed 83886080"}} {
			_, _, err := d.ResolveModifiers(tc.mods...)
			bitDiagnostic(t, err, "22023", tc.message)
		}
		u := d.Usage()
		if n, ok := u.Length(); !ok || n != -1 {
			t.Fatal(n, ok)
		}
		declaration, err := u.Declaration()
		want := "BIT VARYING"
		if d.ID() == Bit {
			want = `"bit"`
		}
		if err != nil || declaration != want {
			t.Fatal(declaration, err)
		}
		u.Typmod = 3
		declaration, err = u.Declaration()
		if err != nil || declaration != d.SQLName()+"(3)" {
			t.Fatal(declaration, err)
		}
	}
	_, err := bit.Input(ctx, "2", 3)
	bitDiagnostic(t, err, "22026", "bit string length 1 does not match type bit(3)")
	_, err = bit.Input(ctx, "é", 3)
	bitDiagnostic(t, err, "22026", "bit string length 2 does not match type bit(3)")
	_, err = varying.Input(ctx, "12", 1)
	bitDiagnostic(t, err, "22001", "bit string too long for type bit varying(1)")
	for _, source := range []*Descriptor{bit, varying} {
		for _, target := range []*Descriptor{bit, varying} {
			for _, context := range []CoercionContext{Implicit, Assignment} {
				u := target.Usage()
				u.Typmod = 3
				plan, err := PlanCoercion(source.Usage(), u, context)
				if err != nil {
					t.Fatal(err)
				}
				for _, text := range []string{"", "10", "001", "1010"} {
					v, _ := BitString(source, text)
					got, err := plan.Apply(ctx, v, InputEnvironment{}, OutputEnvironment{})
					if target == bit && len(text) != 3 {
						bitDiagnostic(t, err, "22026", fmt.Sprintf("bit string length %d does not match type bit(3)", len(text)))
						continue
					}
					if target == varying && len(text) > 3 {
						bitDiagnostic(t, err, "22001", "bit string too long for type bit varying(3)")
						continue
					}
					if err != nil || got.typ != target || got.text != text || len(text) > 0 && unsafe.StringData(got.text) != unsafe.StringData(v.text) {
						t.Fatal(got, err)
					}
				}
				got, err := plan.Apply(ctx, Null(source), InputEnvironment{}, OutputEnvironment{})
				if err != nil || !got.null || got.typ != target {
					t.Fatal(got, err)
				}
				foreign, _ := bitDescriptors()
				v, _ := BitString(foreign, "001")
				if _, err := plan.Apply(ctx, v, InputEnvironment{}, OutputEnvironment{}); err == nil {
					t.Fatal("foreign coercion")
				}
			}
		}
	}
	// Released bit/varbit binary casts also admit explicit destination sizing.
	target := varying.Usage()
	target.Typmod = 2
	plan, err := PlanCoercion(bit.Usage(), target, Explicit)
	if err != nil {
		t.Fatal("released explicit BIT conversion refused", err)
	}
	source, _ := BitString(bit, "101")
	got, err := plan.Apply(ctx, source, InputEnvironment{}, OutputEnvironment{})
	if err != nil || got.Type() != varying || got.text != "10" || source.text != "101" {
		t.Fatal("explicit BIT sizing or immutable source", got, err)
	}
}
