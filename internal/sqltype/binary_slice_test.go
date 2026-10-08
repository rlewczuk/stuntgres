package sqltype

import (
	"context"
	"errors"
	"strings"
	"testing"
)

func TestBinarySliceIntervals(t *testing.T) {
	d := byteaDescriptor(t)
	var integer *Descriptor
	for _, typ := range Builtins() {
		if typ.ID() == Int4 {
			integer = typ
		}
	}
	a, _ := Binary(d, "abcdef")
	for _, name := range []string{"substring", "substr"} {
		p, err := BindBinary(name, d.Usage(), d.Usage(), integer.Usage(), integer.Usage())
		if err != nil {
			t.Fatal(err)
		}
		for _, tc := range []struct {
			s, n int64
			want string
		}{{1, 3, "abc"}, {0, 3, "ab"}, {-2, 4, "a"}, {-3, 2, ""}, {2, 0, ""}, {7, 2, ""}, {2, 2147483646, "bcdef"}, {-10, 2147483646, "abcdef"}, {-2147483648, 2147483647, ""}} {
			s, _ := Integer(integer, tc.s)
			n, _ := Integer(integer, tc.n)
			v, err := p.Evaluate(context.Background(), a, s, n)
			if err != nil {
				t.Fatal(name, tc, err)
			}
			got, err := v.Binary()
			if err != nil || got != tc.want || v.IsNull() {
				t.Fatal(name, tc, got, err)
			}
		}
		tail, err := BindBinary(name, d.Usage(), d.Usage(), integer.Usage())
		if err != nil {
			t.Fatal(err)
		}
		for _, tc := range []struct {
			s    int64
			want string
		}{{-2, "abcdef"}, {0, "abcdef"}, {1, "abcdef"}, {2, "bcdef"}, {7, ""}, {2147483647, ""}} {
			s, _ := Integer(integer, tc.s)
			v, err := tail.Evaluate(context.Background(), a, s)
			if err != nil {
				t.Fatal(err)
			}
			got, _ := v.Binary()
			if got != tc.want {
				t.Fatal(tc, got)
			}
		}
	}
}

func TestBinarySliceOverlayAndPosition(t *testing.T) {
	d := byteaDescriptor(t)
	var integer *Descriptor
	for _, typ := range Builtins() {
		if typ.ID() == Int4 {
			integer = typ
		}
	}
	a, _ := Binary(d, "abcdef")
	r, _ := Binary(d, "XY")
	p, err := BindBinary("overlay", d.Usage(), d.Usage(), d.Usage(), integer.Usage(), integer.Usage())
	if err != nil {
		t.Fatal(err)
	}
	for _, tc := range []struct {
		s, n int64
		want string
	}{{3, 3, "abXYf"}, {3, 0, "abXYcdef"}, {3, -2, "abXYabcdef"}, {9, 2, "abcdefXY"}, {3, -2147483648, "abXYabcdef"}} {
		s, _ := Integer(integer, tc.s)
		n, _ := Integer(integer, tc.n)
		v, err := p.Evaluate(context.Background(), a, r, s, n)
		if err != nil {
			t.Fatal(tc, err)
		}
		got, _ := v.Binary()
		if got != tc.want {
			t.Fatal(tc, got)
		}
	}
	search, err := BindBinary("position", integer.Usage(), d.Usage(), d.Usage())
	if err != nil {
		t.Fatal(err)
	}
	for _, tc := range []struct {
		hay, needle string
		want        int64
	}{{"", "", 1}, {"abc", "", 1}, {"", "x", 0}, {"ababa", "aba", 1}, {"aaaa", "aa", 1}, {"\x00\xc3\xa9\xff", "\xa9\xff", 3}, {"abc", "z", 0}, {"a", "ab", 0}, {strings.Repeat("x", 4095) + "abc", "abc", 4096}} {
		h, _ := Binary(d, tc.hay)
		n, _ := Binary(d, tc.needle)
		v, err := search.Evaluate(context.Background(), h, n)
		if err != nil {
			t.Fatal(err)
		}
		got, _ := v.Integer()
		if got != tc.want {
			t.Fatal(tc, got)
		}
	}
}

func TestBinarySliceStrictOwnersAndCancellation(t *testing.T) {
	d := byteaDescriptor(t)
	var integer *Descriptor
	for _, typ := range Builtins() {
		if typ.ID() == Int4 {
			integer = typ
		}
	}
	for _, name := range []string{"substr", "substring", "overlay", "position"} {
		usages := []Usage{d.Usage(), integer.Usage(), integer.Usage()}
		result := d.Usage()
		if name == "overlay" {
			usages = []Usage{d.Usage(), d.Usage(), integer.Usage(), integer.Usage()}
		}
		if name == "position" {
			usages = []Usage{d.Usage(), d.Usage()}
			result = integer.Usage()
		}
		p, err := BindBinary(name, result, usages...)
		if err != nil {
			t.Fatal(err)
		}
		var frame [4]Value
		for i, u := range usages {
			if u.Type.ID() == Bytea {
				frame[i], _ = Binary(d, "abc")
			} else {
				frame[i], _ = Integer(integer, 1)
			}
		}
		for i, u := range usages {
			original := frame[i]
			frame[i] = Null(u.Type)
			v, err := p.Evaluate(context.Background(), frame[:len(usages)]...)
			if err != nil || !v.IsNull() || v.Type() != result.Type {
				t.Fatal(name, i, v, err)
			}
			frame[i] = original
		}
		ctx, cancel := context.WithCancel(context.Background())
		cancel()
		if _, err := p.Evaluate(ctx, frame[:len(usages)]...); !errors.Is(err, context.Canceled) {
			t.Fatal(name, err)
		}
		if _, err := p.Evaluate(context.Background(), frame[:len(usages)-1]...); err == nil {
			t.Fatal("accepted short carrier", name)
		}
		wrong := usages[0]
		usages[0] = integer.Usage()
		if _, err := BindBinary(name, result, usages...); err == nil {
			t.Fatal("accepted wrong signature", name)
		}
		usages[0] = wrong
	}
}

func TestBinarySliceSearchCheckpointsAndAllocations(t *testing.T) {
	d := byteaDescriptor(t)
	var integer *Descriptor
	for _, typ := range Builtins() {
		if typ.ID() == Int4 {
			integer = typ
		}
	}
	p, err := BindBinary("position", integer.Usage(), d.Usage(), d.Usage())
	if err != nil {
		t.Fatal(err)
	}
	a, _ := Binary(d, strings.Repeat("ab", 20000))
	needle, _ := Binary(d, "xyz")
	ctx := &mathCheckpointContext{Context: context.Background(), remaining: 3}
	if _, err := p.Evaluate(ctx, a, needle); !errors.Is(err, context.Canceled) {
		t.Fatal(err)
	}
	sub, err := BindBinary("substr", d.Usage(), d.Usage(), integer.Usage(), integer.Usage())
	if err != nil {
		t.Fatal(err)
	}
	s, _ := Integer(integer, 2)
	n, _ := Integer(integer, 3)
	if allocs := testing.AllocsPerRun(100, func() {
		if _, err := sub.Evaluate(context.Background(), a, s, n); err != nil {
			panic(err)
		}
	}); allocs != 0 {
		t.Fatal("slice allocates", allocs)
	}
	if allocs := testing.AllocsPerRun(100, func() {
		if _, err := p.Evaluate(context.Background(), a, needle); err != nil {
			panic(err)
		}
	}); allocs != 0 {
		t.Fatal("search allocates", allocs)
	}
}
