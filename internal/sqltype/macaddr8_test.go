package sqltype

import (
	"context"
	"errors"
	"strings"
	"testing"

	"stuntgres/internal/sqlstate"
)

func TestMACADDR8InputOutput(t *testing.T) {
	ctx := context.Background()
	d := testTypes()[Macaddr8]
	for _, tc := range []struct {
		input  string
		bits   uint64
		output string
	}{
		{"08002b010203", 0x08002bfffe010203, "08:00:2b:ff:fe:01:02:03"},
		{"08002b0102030405", 0x08002b0102030405, "08:00:2b:01:02:03:04:05"},
		{"0000000000000000", 0, "00:00:00:00:00:00:00:00"},
		{"8000000000000000", 1 << 63, "80:00:00:00:00:00:00:00"},
		{"ffffffffffffffff", ^uint64(0), "ff:ff:ff:ff:ff:ff:ff:ff"},
	} {
		v, err := d.Input(ctx, tc.input, -1)
		if err != nil {
			t.Fatal(err)
		}
		if bits, err := v.MACADDR8(); err != nil || bits != tc.bits {
			t.Fatal(bits, err)
		}
		if output, err := d.Output(ctx, v); err != nil || output != tc.output {
			t.Fatal(output, err)
		}
		if literal, err := d.SQLLiteral(ctx, v); err != nil || literal != "MACADDR8 '"+tc.output+"'" {
			t.Fatal(literal, err)
		}
		back, err := d.Input(ctx, tc.output, -1)
		if err != nil || !SameDatum(v, back) {
			t.Fatal(back, err)
		}
	}
	if literal, err := d.SQLLiteral(ctx, Null(d)); err != nil || literal != "CAST(NULL AS MACADDR8)" {
		t.Fatal(literal, err)
	}
	if n := testing.AllocsPerRun(100, func() {
		v, err := d.Input(ctx, "08002b0102030405", -1)
		if err != nil {
			panic(err)
		}
		if bits, err := v.MACADDR8(); err != nil || bits != 0x08002b0102030405 {
			panic("inline access")
		}
	}); n != 0 {
		t.Fatal("parse/access allocations", n)
	}
}

func TestMACADDR8Scanner(t *testing.T) {
	d := testTypes()[Macaddr8]
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	for _, input := range []string{strings.Repeat(" ", 8192) + "08002b010203", "08002b010203" + strings.Repeat(" ", 8192)} {
		if _, err := d.Input(ctx, input, -1); !errors.Is(err, context.Canceled) {
			t.Fatal(err)
		}
	}
	// The pair decoder has no scanf sign/prefix/range semantics.
	for n := range 256 {
		v, ok := macaddr8Nibble(byte(n))
		valid := n >= '0' && n <= '9' || n >= 'a' && n <= 'f' || n >= 'A' && n <= 'F'
		if ok != valid || ok && v > 15 {
			t.Fatal(n, v, ok)
		}
	}
}

func TestMACADDR8Datum(t *testing.T) {
	types := testTypes()
	d := types[Macaddr8]
	for _, owner := range []*Descriptor{nil, types[Macaddr], types[Oid]} {
		if _, err := MACADDR8Value(owner, 0); err == nil {
			t.Fatal(owner)
		}
	}
	for _, v := range []Value{{}, Null(d), {typ: types[Macaddr]}} {
		if _, err := v.MACADDR8(); err == nil {
			t.Fatal(v)
		}
	}
	for _, bits := range []uint64{0, 1 << 63, ^uint64(0)} {
		a, err := MACADDR8Value(d, bits)
		b, _ := MACADDR8Value(d, bits)
		if err != nil || !SameDatum(a, b) || SameDatum(a, Null(d)) {
			t.Fatal(bits, err)
		}
	}
	for _, pair := range [][2]ID{{Int8, Macaddr8}, {Macaddr8, Int8}} {
		if _, err := PlanCoercion(types[pair[0]].Usage(), types[pair[1]].Usage(), Explicit); err == nil {
			t.Fatal(pair)
		}
	}
}

func TestMACADDR8Comparison(t *testing.T) {
	types := testTypes()
	d := types[Macaddr8]
	if _, ok := DefaultIndexClass(d); ok {
		t.Fatal("default index class")
	}
	for op := CompareEqual; op <= CompareGreaterEqual; op++ {
		p, err := BindComparisonPredicate(op, d, d, types[Bool], CollationRef{}, false)
		if err != nil {
			t.Fatal(err)
		}
		a, _ := MACADDR8Value(d, 0x7fffffffffffffff)
		b, _ := MACADDR8Value(d, 0x8000000000000000)
		v, err := p.Evaluate(context.Background(), a, b, InputEnvironment{})
		if err != nil {
			t.Fatal(err)
		}
		truth, err := v.Boolean()
		if err != nil || truth != []bool{false, true, true, true, false, false}[op] {
			t.Fatal(op, truth, err)
		}
		for _, pair := range [][2]Value{{Null(d), a}, {a, Null(d)}} {
			v, err := p.Evaluate(context.Background(), pair[0], pair[1], InputEnvironment{})
			if err != nil || !v.IsNull() || v.Type() != types[Bool] {
				t.Fatal(v, err)
			}
		}
		if n := testing.AllocsPerRun(100, func() {
			if _, err := p.Evaluate(context.Background(), a, b, InputEnvironment{}); err != nil {
				panic(err)
			}
		}); n != 0 {
			t.Fatal("comparison allocations", n)
		}
	}
}

func TestMACADDR8Descriptor(t *testing.T) {
	d := testTypes()[Macaddr8]
	if !d.Concrete() || d.AssociatedArray() != 775 || d.DatabaseName() != "MACADDR8" {
		t.Fatal(d)
	}
	f, ok := d.PGTypeMetadata()
	if !ok || f.Length != 8 || f.ByValue || f.Alignment != "i" || f.Storage != "p" || f.Category != "U" || f.Preferred || f.Input != 4110 || f.Output != 4111 || f.Receive != 3446 || f.Send != 3447 {
		t.Fatal(f, ok)
	}
	for _, err := range []error{d.CheckBinaryInput(), d.CheckBinaryOutput()} {
		var diagnostic *sqlstate.Error
		if !errors.As(err, &diagnostic) || diagnostic.Code != "0A000" {
			t.Fatal(err)
		}
	}
}

func TestMACADDR8ArrayBoundary(t *testing.T) {
	types := testTypes()
	d := types[Macaddr8]
	if a := types[775]; a == nil || a.Concrete() || a.Element() != d {
		t.Fatal(a)
	}
	domain, err := Domain(60001, "mac8_domain", "public.mac8_domain", d.Usage())
	if err != nil {
		t.Fatal(err)
	}
	for _, element := range []*Descriptor{d, domain} {
		if a, err := Array(60002, "_mac8_boundary", element); err == nil || a != nil {
			t.Fatal(a, err)
		}
	}
}
