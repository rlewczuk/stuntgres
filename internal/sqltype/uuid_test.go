package sqltype

import (
	"context"
	"errors"
	"reflect"
	"strings"
	"stuntgres/internal/sqlstate"
	"testing"
)

func uuidDescriptor() *Descriptor {
	for _, d := range Builtins() {
		if d.ID() == UUID {
			return d
		}
	}
	panic("UUID missing")
}

func TestUUIDCodecFormsAndGuards(t *testing.T) {
	ctx := context.Background()
	d := uuidDescriptor()
	const compact = "a0eebc999c0b4ef8bb6d6bb9bd380a11"
	const canonical = "a0eebc99-9c0b-4ef8-bb6d-6bb9bd380a11"
	want := [16]byte{0xa0, 0xee, 0xbc, 0x99, 0x9c, 0x0b, 0x4e, 0xf8, 0xbb, 0x6d, 0x6b, 0xb9, 0xbd, 0x38, 0x0a, 0x11}
	if d.ID() != 2950 || d.Name() != "uuid" || d.SQLName() != "UUID" || d.DatabaseName() != "UUID" || d.Usage().Typmod != -1 || d.Usage().Collation != (CollationRef{}) {
		t.Fatal(d, d.Usage())
	}
	for mask := range 128 {
		var b strings.Builder
		for i := range 8 {
			b.WriteString(compact[i*4 : i*4+4])
			if i < 7 && mask&(1<<i) != 0 {
				b.WriteByte('-')
			}
		}
		for _, s := range []string{b.String(), strings.ToUpper(b.String()), "{" + b.String() + "}", "{" + strings.ToUpper(b.String()) + "}"} {
			v, err := d.Input(ctx, s, -1)
			if err != nil {
				t.Fatal(s, err)
			}
			got, err := v.UUID()
			if err != nil || got != want {
				t.Fatal(got, err)
			}
			got[0] = 0
			text, err := d.Output(ctx, v)
			if err != nil || text != canonical {
				t.Fatal(text, err)
			}
			literal, err := d.SQLLiteral(ctx, v)
			if err != nil || literal != "'"+canonical+"'" {
				t.Fatal(literal, err)
			}
		}
	}
	for _, s := range []string{strings.Repeat("0", 32), strings.Repeat("f", 32), "123456789abcdef0123456789abcdef0"} {
		if _, err := d.Input(ctx, s, -1); err != nil {
			t.Fatal(s, err)
		}
	}
	for _, s := range []string{"", " " + compact, compact + " ", "urn:uuid:" + compact, "{{" + compact + "}}", "{" + compact, compact + "}", compact[:31], compact + "0", "g" + compact[1:], "Ａ" + compact[1:], compact[:2] + "-" + compact[2:], compact[:4] + "--" + compact[4:], compact + "-", compact + "suffix", "+" + compact} {
		_, err := d.Input(ctx, s, -1)
		var got *sqlstate.Error
		wantErr := sqlstate.Error{Severity: "ERROR", SeverityUnlocalized: "ERROR", Code: "22P02", Message: "invalid input syntax for type uuid: \"" + s + "\""}
		if !errors.As(err, &got) || *got != wantErr {
			t.Fatalf("%q: %#v %v", s, got, err)
		}
	}
	v, _ := UUIDValue(d, want)
	if _, err := UUIDValue(nil, want); err == nil {
		t.Fatal("nil constructor")
	}
	if _, err := UUIDValue(Builtins()[0], want); err == nil {
		t.Fatal("wrong constructor")
	}
	for _, bad := range []Value{{}, Null(d), Boolean(Builtins()[0], true)} {
		if _, err := bad.UUID(); err == nil {
			t.Fatal("invalid accessor")
		}
		if _, err := d.Output(ctx, bad); err == nil {
			t.Fatal("invalid output")
		}
	}
	foreign := uuidDescriptor()
	if _, err := foreign.Output(ctx, v); err == nil {
		t.Fatal("foreign output")
	}
	if _, err := foreign.SQLLiteral(ctx, v); err == nil {
		t.Fatal("foreign literal")
	}
	if _, err := d.Input(ctx, compact, 0); err == nil {
		t.Fatal("typmod")
	}
	canceled, cancel := context.WithCancel(ctx)
	cancel()
	for _, f := range []func() error{func() error { _, e := d.Input(canceled, compact, -1); return e }, func() error { _, e := d.Output(canceled, v); return e }, func() error { _, e := d.SQLLiteral(canceled, v); return e }} {
		if !errors.Is(f(), context.Canceled) {
			t.Fatal("cancellation")
		}
	}
	if text, err := d.SQLLiteral(ctx, Null(d)); err != nil || text != "NULL" {
		t.Fatal(text, err)
	}
	if !reflect.DeepEqual(v.uuid, want) {
		t.Fatal("accessor mutated storage")
	}
}

func TestUUIDComparisonAndNull(t *testing.T) {
	ctx := context.Background()
	d := uuidDescriptor()
	p, err := BindComparison(d, d)
	if err != nil {
		t.Fatal(err)
	}
	for _, pair := range [][2]string{{"00000001000000000000000000000000", "00000100000000000000000000000000"}, {"7fffffffffffffffffffffffffffffff", "80000000000000000000000000000000"}, {"00000000000000000000000000000000", "00000000000000000000000000000001"}} {
		a, _ := d.Input(ctx, pair[0], -1)
		b, _ := d.Input(ctx, pair[1], -1)
		for _, tc := range []struct {
			a, b Value
			want int
		}{{a, b, -1}, {b, a, 1}, {a, a, 0}} {
			got, err := p.Compare(ctx, tc.a, tc.b, InputEnvironment{})
			if err != nil || got != tc.want {
				t.Fatal(got, err)
			}
		}
	}
	a, _ := d.Input(ctx, "a0eebc999c0b4ef8bb6d6bb9bd380a11", -1)
	b, _ := d.Input(ctx, "{A0EEBC99-9C0B-4EF8-BB6D-6BB9BD380A11}", -1)
	if n, err := p.Compare(ctx, a, b, InputEnvironment{}); err != nil || n != 0 {
		t.Fatal(n, err)
	}
	boolType := Builtins()[0]
	eq, err := BindComparisonPredicate(CompareEqual, d, d, boolType)
	if err != nil {
		t.Fatal(err)
	}
	for _, pair := range [][2]Value{{Null(d), a}, {a, Null(d)}} {
		v, err := eq.Evaluate(ctx, pair[0], pair[1], InputEnvironment{})
		if err != nil || !v.IsNull() || v.Type() != boolType {
			t.Fatal(v, err)
		}
	}
	if _, err := p.Compare(ctx, a, Null(d), InputEnvironment{}); err == nil {
		t.Fatal("NULL comparison")
	}
	foreign, _ := uuidDescriptor().Input(ctx, "00000000000000000000000000000000", -1)
	if _, err := p.Compare(ctx, a, foreign, InputEnvironment{}); err == nil {
		t.Fatal("foreign comparison")
	}
	canceled, cancel := context.WithCancel(ctx)
	cancel()
	if _, err := p.Compare(canceled, a, b, InputEnvironment{}); !errors.Is(err, context.Canceled) {
		t.Fatal(err)
	}
}
