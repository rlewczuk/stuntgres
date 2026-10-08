package sqltype

import (
	"context"
	"errors"
	"strings"
	"stuntgres/internal/sqlstate"
	"testing"
)

func TestBinaryBasicProvider(t *testing.T) {
	d := byteaDescriptor(t)
	var integer *Descriptor
	for _, v := range Builtins() {
		if v.ID() == Int4 {
			integer = v
		}
	}
	for _, tc := range []struct {
		name, a, b, want string
		count            int64
	}{
		{"||", "\x00\xff\x80A\x00\x80B\xff\x00", "\xff\x80\x00", "\x00\xff\x80A\x00\x80B\xff\x00\xff\x80\x00", 0},
		{"btrim", "\x00\xff\x80A\x00\x80B\xff\x00", "\xff\x80\x00", "A\x00\x80B", 0},
		{"ltrim", "\x00\xff\x80A\x00\x80B\xff\x00", "\xff\x80\x00", "A\x00\x80B\xff\x00", 0},
		{"rtrim", "\x00\xff\x80A\x00\x80B\xff\x00", "\xff\x80\x00", "\x00\xff\x80A\x00\x80B", 0},
		{"btrim", "\x01\x02\x01\x03\x02\x01", "\x01\x01\x01\x02", "\x03", 0},
		{"ltrim", "\x01\x02\x01\x03\x02\x01", "\x02\x01", "\x03\x02\x01", 0},
		{"rtrim", "\x01\x02\x01\x03\x02\x01", "\x02\x01", "\x01\x02\x01\x03", 0},
		{"btrim", "\x01\x02\x01", "\x02\x01", "", 0},
		{"btrim", " A ", "\x00", " A ", 0},
		{"length", "\xc3\xa9\xf0\x9f\x98\x80", "", "", 6},
		{"octet_length", "\xc3\xa9\xf0\x9f\x98\x80", "", "", 6},
		{"bit_length", "\xc3\xa9\xf0\x9f\x98\x80", "", "", 48},
	} {
		t.Run(tc.name+tc.a, func(t *testing.T) {
			u, args := d.Usage(), []Usage{d.Usage(), d.Usage()}
			if tc.name == "length" || tc.name == "octet_length" || tc.name == "bit_length" {
				u = integer.Usage()
				args = args[:1]
			}
			p, err := BindBinary(tc.name, u, args...)
			if err != nil {
				t.Fatal(err)
			}
			a, _ := Binary(d, tc.a)
			b, _ := Binary(d, tc.b)
			got, err := p.Evaluate(context.Background(), a, b)
			if err != nil {
				t.Fatal(err)
			}
			if got.Type() != u.Type || p.ResultUsage() != u || got.IsNull() {
				t.Fatal(got, p)
			}
			if u.Type.ID() == Bytea {
				raw, err := got.Binary()
				if err != nil || raw != tc.want {
					t.Fatalf("%q want %q: %v", raw, tc.want, err)
				}
			} else {
				n, err := got.Integer()
				if err != nil || n != tc.count {
					t.Fatal(n, tc.count, err)
				}
			}
			null, err := p.Evaluate(context.Background(), Null(d), b)
			if err != nil || !null.IsNull() || null.Type() != u.Type {
				t.Fatal(null, err)
			}
			if len(args) == 2 {
				null, err = p.Evaluate(context.Background(), a, Null(d))
				if err != nil || !null.IsNull() || null.Type() != u.Type {
					t.Fatal(null, err)
				}
			}
		})
	}
	for _, name := range []string{"||", "btrim", "ltrim", "rtrim"} {
		p, err := BindBinary(name, d.Usage(), d.Usage(), d.Usage())
		if err != nil {
			t.Fatal(err)
		}
		empty, _ := Binary(d, "")
		nonempty, _ := Binary(d, "\x00\xff")
		for _, pair := range [][2]Value{{empty, empty}, {empty, nonempty}, {nonempty, empty}} {
			v, err := p.Evaluate(context.Background(), pair[0], pair[1])
			if err != nil || v.IsNull() {
				t.Fatal(v, err)
			}
			want := pair[0].text
			if name == "||" {
				want += pair[1].text
			}
			if v.text != want {
				t.Fatal(v.text, want)
			}
		}
	}
}

func TestBinaryBasicInvariantsOverflowAndAllocations(t *testing.T) {
	d := byteaDescriptor(t)
	var integer *Descriptor
	for _, v := range Builtins() {
		if v.ID() == Int4 {
			integer = v
		}
	}
	for _, args := range [][]Usage{nil, {d.Usage(), d.Usage()}, {integer.Usage()}} {
		if _, err := BindBinary("length", integer.Usage(), args...); err == nil {
			t.Fatal("accepted invalid signature")
		}
	}
	other := byteaDescriptor(t)
	if _, err := BindBinary("btrim", d.Usage(), d.Usage(), other.Usage()); err == nil {
		t.Fatal("accepted foreign owner")
	}
	if _, err := BindBinary("btrim", other.Usage(), d.Usage(), d.Usage()); err == nil {
		t.Fatal("accepted foreign result owner")
	}
	for _, tc := range []struct {
		count int64
		fail  bool
	}{{268435455, false}, {268435456, true}} {
		v, err := binaryLength(integer, tc.count, true)
		if tc.fail {
			var se *sqlstate.Error
			if !errors.As(err, &se) || se.Code != "22003" || se.Message != "integer out of range" {
				t.Fatal(err)
			}
		} else {
			n, _ := v.Integer()
			if err != nil || n != 2147483640 {
				t.Fatal(n, err)
			}
		}
	}
	a, _ := Binary(d, "\x00\xffA\x00")
	set, _ := Binary(d, "\xff\x00")
	empty, _ := Binary(d, "")
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	for _, name := range []string{"length", "octet_length", "bit_length", "btrim", "ltrim", "rtrim", "||"} {
		u, args := d.Usage(), []Usage{d.Usage(), d.Usage()}
		if name == "length" || name == "octet_length" || name == "bit_length" {
			u = integer.Usage()
			args = args[:1]
		}
		p, err := BindBinary(name, u, args...)
		if err != nil {
			t.Fatal(err)
		}
		if _, err := p.Evaluate(ctx, a, set); !errors.Is(err, context.Canceled) {
			t.Fatal(err)
		}
		if _, err := p.Evaluate(context.Background(), Null(other), set); err == nil {
			t.Fatal("accepted foreign value")
		}
		b := set
		if name == "||" {
			b = empty
		}
		if n := testing.AllocsPerRun(100, func() {
			if _, err := p.Evaluate(context.Background(), a, b); err != nil {
				panic(err)
			}
		}); n != 0 {
			t.Fatal(name, n)
		}
	}
	p, _ := BindBinary("||", d.Usage(), d.Usage(), d.Usage())
	if n := testing.AllocsPerRun(100, func() {
		if _, err := p.Evaluate(context.Background(), a, set); err != nil {
			panic(err)
		}
	}); n != 1 {
		t.Fatal("concat allocations", n)
	}
}

func TestBinaryBasicTrimCheckpointCancellation(t *testing.T) {
	d := byteaDescriptor(t)
	for _, tc := range []struct{ name, a, b string }{
		{"btrim", "A", strings.Repeat("\x00", 8192)},
		{"ltrim", strings.Repeat("\x00", 8192), "\x00"},
		{"rtrim", strings.Repeat("\x00", 8192), "\x00"},
	} {
		p, err := BindBinary(tc.name, d.Usage(), d.Usage(), d.Usage())
		if err != nil {
			t.Fatal(err)
		}
		a, _ := Binary(d, tc.a)
		b, _ := Binary(d, tc.b)
		ctx := &mathCheckpointContext{Context: context.Background(), remaining: 3}
		if _, err := p.Evaluate(ctx, a, b); !errors.Is(err, context.Canceled) {
			t.Fatal(tc.name, err)
		}
	}
}
