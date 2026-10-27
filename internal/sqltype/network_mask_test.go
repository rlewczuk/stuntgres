package sqltype

import (
	"context"
	"errors"
	"fmt"
	"testing"

	"stuntgres/internal/util"
)

func networkMaskPlan(t *testing.T, k NetworkFormatKind, input ID) (NetworkFormat, map[ID]*Descriptor) {
	t.Helper()
	types := testTypes()
	var right *Descriptor
	if k == NetworkSetMasklen {
		right = types[Int4]
	} else if k.Arity() == 2 {
		right = types[input]
	}
	p, err := BindNetworkFormat(k, types[k.ResultFor(input)], types[input], right)
	if err != nil {
		t.Fatal(err)
	}
	return p, types
}

func TestNetworkMaskValues(t *testing.T) {
	ctx := context.Background()
	for _, tc := range []struct {
		k       NetworkFormatKind
		input   ID
		a, b, w string
		n       int64
	}{
		{NetworkHostmask, Inet, "192.168.23.20/30", "", "0.0.0.3", 0},
		{NetworkNetmask, Inet, "192.168.1.5/24", "", "255.255.255.0", 0},
		{NetworkNetwork, Inet, "192.168.1.5/24", "", "192.168.1.0/24", 0},
		{NetworkSetMasklen, Inet, "192.168.1.5/24", "", "192.168.1.5/16", 16},
		{NetworkSetMasklen, Cidr, "192.168.1.0/24", "", "192.168.0.0/16", 16},
		{NetworkMerge, Inet, "192.168.1.5/24", "192.168.2.5/24", "192.168.0.0/22", 0},
		{NetworkHostmask, Inet, "2001:db8::ffff/113", "", "::7fff", 0},
		{NetworkNetmask, Inet, "2001:db8::ffff/113", "", "ffff:ffff:ffff:ffff:ffff:ffff:ffff:8000", 0},
		{NetworkNetwork, Inet, "2001:db8::ffff/113", "", "2001:db8::8000/113", 0},
		{NetworkSetMasklen, Inet, "2001:db8::ffff/113", "", "2001:db8::ffff", -1},
		{NetworkSetMasklen, Cidr, "2001:db8::8000/113", "", "2001:db8::8000/128", -1},
		{NetworkMerge, Inet, "::1", "::2", "::/126", 0},
		{NetworkMerge, Inet, "10.1.2.3/8", "10.9.8.7/24", "10.0.0.0/8", 0},
		{NetworkMerge, Inet, "::ffff:1.2.3.4/120", "::ffff:1.2.3.5/128", "::ffff:1.2.3.0/120", 0},
	} {
		p, types := networkMaskPlan(t, tc.k, tc.input)
		a := networkOperatorValue(t, types[tc.input], tc.a)
		var b Value
		if tc.k == NetworkSetMasklen {
			b, _ = Integer(types[Int4], tc.n)
		} else if tc.k.Arity() == 2 {
			b = networkOperatorValue(t, types[Inet], tc.b)
		}
		beforeA, beforeB := a, b
		var got Value
		var err error
		if tc.k.Arity() == 1 {
			got, err = p.Unary(ctx, a)
		} else {
			got, err = p.Binary(ctx, a, b)
		}
		if err != nil || got.typ != types[tc.k.ResultFor(tc.input)] || got.null || !got.network.valid(got.typ.id == Cidr) || got.network.output(got.typ.id == Cidr) != tc.w || a != beforeA || b != beforeB {
			t.Fatal(tc, got, err)
		}
	}
}

func TestNetworkMaskBoundaries(t *testing.T) {
	ctx := context.Background()
	for _, family := range []byte{4, 6} {
		width := 32
		if family == 6 {
			width = 128
		}
		prefixes := []int{0, 1, 7, 8, 9, 24, 25, 31, 32}
		if family == 6 {
			prefixes = []int{0, 1, 63, 64, 65, 95, 96, 97, 111, 112, 113, 127, 128}
		}
		for _, prefix := range prefixes {
			var source networkDatum
			source.family = family
			source.prefix = uint8(prefix)
			for i := range width / 8 {
				source.address[i] = byte(i*37 + 0xa5)
			}
			// A deliberately separate bit-by-bit expected-byte model. It does not use
			// masked(), a production evaluator, or canonical string output as an oracle.
			network, host, mask := source, source, source
			host.prefix = uint8(width)
			mask.prefix = uint8(width)
			host.address = [16]byte{}
			mask.address = [16]byte{}
			for bit := range width {
				flag := byte(1 << uint(7-bit%8))
				if bit < prefix {
					mask.address[bit/8] |= flag
				} else {
					host.address[bit/8] |= flag
					network.address[bit/8] &= ^flag
				}
			}
			for _, tc := range []struct {
				k    NetworkFormatKind
				want networkDatum
			}{{NetworkHostmask, host}, {NetworkNetmask, mask}, {NetworkNetwork, network}} {
				p, types := networkMaskPlan(t, tc.k, Inet)
				in := Value{typ: types[Inet], network: source}
				before := in
				got, err := p.Unary(ctx, in)
				if err != nil || got.network != tc.want || in != before {
					t.Fatal(family, prefix, tc.k, got, err)
				}
			}
			for _, input := range []ID{Inet, Cidr} {
				p, types := networkMaskPlan(t, NetworkSetMasklen, input)
				original := source
				if input == Cidr {
					original = network
				}
				in := Value{typ: types[input], network: original}
				for _, n := range []int{0, prefix, width, -1} {
					arg, _ := Integer(types[Int4], int64(n))
					want := original
					want.prefix = uint8(n)
					if n == -1 {
						want.prefix = uint8(width)
					}
					if input == Cidr {
						for bit := int(want.prefix); bit < width; bit++ {
							want.address[bit/8] &= ^(byte(1) << uint(7-bit%8))
						}
					}
					got, err := p.Binary(ctx, in, arg)
					if err != nil || got.network != want || got.typ != types[input] || in.network != original {
						t.Fatal(family, prefix, input, n, got, err)
					}
				}
			}
		}
	}
}

func TestNetworkMaskValidation(t *testing.T) {
	types, foreign := testTypes(), testTypes()
	for _, tc := range []struct {
		k    NetworkFormatKind
		left ID
	}{{NetworkHostmask, Inet}, {NetworkNetmask, Inet}, {NetworkNetwork, Inet}, {NetworkMerge, Inet}, {NetworkSetMasklen, Inet}, {NetworkSetMasklen, Cidr}} {
		p, _ := networkMaskPlan(t, tc.k, tc.left)
		right := (*Descriptor)(nil)
		if tc.k == NetworkSetMasklen {
			right = types[Int4]
		} else if tc.k.Arity() == 2 {
			right = types[Inet]
		}
		for _, bad := range []struct{ result, left, right *Descriptor }{
			{nil, types[tc.left], right}, {types[Text], types[tc.left], right}, {types[tc.k.ResultFor(tc.left)], nil, right}, {types[tc.k.ResultFor(tc.left)], types[Text], right},
			{foreign[tc.k.ResultFor(tc.left)], types[tc.left], right},
		} {
			if _, err := BindNetworkFormat(tc.k, bad.result, bad.left, bad.right); err == nil {
				t.Fatal("bad signature", tc, bad)
			}
		}
		if right != nil {
			for _, bad := range []*Descriptor{nil, types[Text], foreign[right.id]} {
				if _, err := BindNetworkFormat(tc.k, types[tc.k.ResultFor(tc.left)], types[tc.left], bad); err == nil {
					t.Fatal("bad right descriptor", tc, bad)
				}
			}
		}
		a := networkOperatorValue(t, p.left, "192.168.1.0/24")
		bad := a
		bad.network.prefix = 33
		foreignValue := networkOperatorValue(t, foreign[tc.left], "192.168.1.0/24")
		if tc.k.Arity() == 1 {
			for _, in := range []Value{bad, foreignValue} {
				if _, err := p.Unary(context.Background(), in); err == nil {
					t.Fatal("bad unary datum")
				}
			}
			if _, err := p.Binary(context.Background(), a, a); err == nil {
				t.Fatal("wrong arity")
			}
		} else {
			arg := a
			if tc.k == NetworkSetMasklen {
				arg, _ = Integer(p.right, 24)
			}
			for _, in := range []Value{bad, foreignValue} {
				if _, err := p.Binary(context.Background(), in, arg); err == nil {
					t.Fatal("bad binary datum")
				}
			}
			if _, err := p.Binary(context.Background(), a, Null(foreign[p.right.id])); err == nil {
				t.Fatal("foreign typed null")
			}
			if _, err := p.Unary(context.Background(), a); err == nil {
				t.Fatal("wrong arity")
			}
		}
	}
	if _, err := BindNetworkFormat(0, types[Inet], types[Inet], nil); err == nil {
		t.Fatal("unknown kind")
	}
	for _, input := range []ID{Inet, Cidr} {
		p, types := networkMaskPlan(t, NetworkSetMasklen, input)
		for _, tc := range []struct {
			ip string
			n  int64
		}{{"192.168.1.0/24", -2}, {"192.168.1.0/24", 33}, {"2001:db8::/64", 129}, {"::/0", -2147483648}, {"::/0", 2147483647}} {
			a := networkOperatorValue(t, types[input], tc.ip)
			n, _ := Integer(types[Int4], tc.n)
			_, err := p.Binary(context.Background(), a, n)
			var d interface{ SQLState() string }
			if !errors.As(err, &d) || d.SQLState() != "22023" || err.Error() != "stuntgres: "+fmt.Sprintf("invalid mask length: %d", tc.n) {
				t.Fatal(tc, err)
			}
		}
	}
	p, types := networkMaskPlan(t, NetworkMerge, Inet)
	_, err := p.Binary(context.Background(), networkOperatorValue(t, types[Inet], "1.2.3.4"), networkOperatorValue(t, types[Inet], "::ffff:1.2.3.4"))
	var d interface{ SQLState() string }
	if !errors.As(err, &d) || d.SQLState() != "22023" || err.Error() != "stuntgres: cannot merge addresses from different families" {
		t.Fatal(err)
	}
}

func TestNetworkMaskCancellation(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	for _, k := range []NetworkFormatKind{NetworkHostmask, NetworkNetmask, NetworkNetwork, NetworkMerge, NetworkSetMasklen} {
		inputs := []ID{Inet}
		if k == NetworkSetMasklen {
			inputs = append(inputs, Cidr)
		}
		for _, input := range inputs {
			p, types := networkMaskPlan(t, k, input)
			a := networkOperatorValue(t, types[input], "192.168.1.0/24")
			if k.Arity() == 1 {
				for _, v := range []Value{a, Null(p.left)} {
					if _, err := p.Unary(ctx, v); !errors.Is(err, context.Canceled) {
						t.Fatal(k, err)
					}
				}
			} else {
				b := a
				if k == NetworkSetMasklen {
					b, _ = Integer(types[Int4], 24)
				}
				for _, pair := range [][2]Value{{a, b}, {Null(p.left), b}, {a, Null(p.right)}} {
					if _, err := p.Binary(ctx, pair[0], pair[1]); !errors.Is(err, context.Canceled) {
						t.Fatal(k, err)
					}
				}
			}
		}
	}
}

func TestNetworkMaskStrictDemand(t *testing.T) {
	for _, k := range []NetworkFormatKind{NetworkHostmask, NetworkNetmask, NetworkNetwork, NetworkMerge, NetworkSetMasklen} {
		inputs := []ID{Inet}
		if k == NetworkSetMasklen {
			inputs = append(inputs, Cidr)
		}
		for _, input := range inputs {
			p, types := networkMaskPlan(t, k, input)
			if k.Arity() == 1 {
				v, err := p.Unary(context.Background(), Null(types[input]))
				if err != nil || !v.null || v.typ != p.result {
					t.Fatal(k, v, err)
				}
			} else {
				a := networkOperatorValue(t, types[input], "192.168.1.0/24")
				b := networkOperatorValue(t, types[Inet], "::1")
				if k == NetworkSetMasklen {
					b, _ = Integer(types[Int4], 129)
				}
				for _, pair := range [][2]Value{{Null(p.left), b}, {a, Null(p.right)}} {
					v, err := p.Binary(context.Background(), pair[0], pair[1])
					if err != nil || !v.null || v.typ != p.result {
						t.Fatal(k, v, err)
					}
				}
			}
		}
	}
}

func TestNetworkMaskAllocations(t *testing.T) {
	for _, tc := range []struct {
		k     NetworkFormatKind
		input ID
	}{{NetworkHostmask, Inet}, {NetworkNetmask, Inet}, {NetworkNetwork, Inet}, {NetworkMerge, Inet}, {NetworkSetMasklen, Inet}, {NetworkSetMasklen, Cidr}} {
		p, types := networkMaskPlan(t, tc.k, tc.input)
		a := networkOperatorValue(t, types[tc.input], "2001:db8::/64")
		b := a
		if tc.k == NetworkSetMasklen {
			b, _ = Integer(types[Int4], 113)
		}
		allocations, bytes := util.AllocationsPerCall(1000, func() {
			var got Value
			var err error
			if tc.k.Arity() == 1 {
				got, err = p.Unary(context.Background(), a)
			} else {
				got, err = p.Binary(context.Background(), a, b)
			}
			if err != nil || got.typ != p.result {
				panic("invalid mask allocation probe")
			}
		})
		if allocations != 0 || bytes != 0 {
			t.Fatal(tc, allocations, bytes)
		}
	}
}
