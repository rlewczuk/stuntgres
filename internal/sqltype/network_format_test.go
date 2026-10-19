package sqltype

import (
	"context"
	"errors"
	"fmt"
	"testing"
)

func networkFormatPlan(t *testing.T, k NetworkFormatKind, input ID) (NetworkFormat, map[ID]*Descriptor) {
	t.Helper()
	types := testTypes()
	var right *Descriptor
	if k.Arity() == 2 {
		right = types[input]
	}
	p, err := BindNetworkFormat(k, types[k.ResultFor(input)], types[input], right)
	if err != nil {
		t.Fatal(err)
	}
	return p, types
}
func TestNetworkFormatValues(t *testing.T) {
	for _, tc := range []struct {
		input, abbrev, host string
		family, mask        int64
	}{
		{"192.168.1.5/24", "192.168.1.5/24", "192.168.1.5", 4, 24}, {"10.1.0.0/32", "10.1.0.0", "10.1.0.0", 4, 32}, {"2001:0DB8:0:0:0:0:0:1/128", "2001:db8::1", "2001:db8::1", 6, 128}, {"::ffff:1.2.3.4/120", "::ffff:1.2.3.4/120", "::ffff:1.2.3.4", 6, 120},
	} {
		for _, k := range []NetworkFormatKind{NetworkAbbrev, NetworkHost, NetworkFamily, NetworkMasklen} {
			p, types := networkFormatPlan(t, k, Inet)
			v := networkOperatorValue(t, types[Inet], tc.input)
			before := v
			got, err := p.Unary(context.Background(), v)
			if err != nil || got.typ != types[k.ResultFor(Inet)] || got.null {
				t.Fatal(tc, k, got, err)
			}
			switch k {
			case NetworkAbbrev:
				if got.text != tc.abbrev {
					t.Fatal(got.text, tc.abbrev)
				}
			case NetworkHost:
				if got.text != tc.host {
					t.Fatal(got.text, tc.host)
				}
			case NetworkFamily:
				if got.integer != tc.family {
					t.Fatal(got.integer, tc.family)
				}
			case NetworkMasklen:
				if got.integer != tc.mask {
					t.Fatal(got.integer, tc.mask)
				}
			}
			if v != before {
				t.Fatal("source mutated")
			}
		}
	}
	p, types := networkFormatPlan(t, NetworkSameFamily, Inet)
	for _, tc := range []struct {
		a, b string
		want bool
	}{{"1.2.3.4/8", "9.8.7.6", true}, {"::1", "::ffff:1.2.3.4", true}, {"1.2.3.4", "::ffff:1.2.3.4", false}} {
		a, b := networkOperatorValue(t, types[Inet], tc.a), networkOperatorValue(t, types[Inet], tc.b)
		beforeA, beforeB := a, b
		got, err := p.Binary(context.Background(), a, b)
		if err != nil || got.typ != types[Bool] || got.null || got.boolean != tc.want {
			t.Fatal(tc, got, err)
		}
		if a != beforeA || b != beforeB {
			t.Fatal("binary source mutated")
		}
	}
}

func TestNetworkFormatAbbreviation(t *testing.T) {
	p, types := networkFormatPlan(t, NetworkAbbrev, Cidr)
	// Explicit manual and pinned18.6 outcomes, never native-derived goldens.
	for _, tc := range []struct{ input, want string }{
		{"10", "10/8"}, {"10.1", "10.1/16"}, {"128", "128.0/16"}, {"192.168/24", "192.168.0/24"}, {"192.168/25", "192.168.0.0/25"}, {"10.1.2.3/32", "10.1.2.3/32"}, {"0.0.0.0/0", "0/0"}, {"2001:4f8:3:ba::/64", "2001:4f8:3:ba/64"}, {"::/0", "::/0"}, {"2001:db8::/32", "2001:db8/32"}, {"2001::/16", "2001::/16"}, {"2001:0:1::/48", "2001::1/48"}, {"2001:0:0:1::/64", "2001::1/64"}, {"2001:db8:0:1:0:2::/112", "2001:db8:0:1::0/112"},
		{"::ffff:0.0.0.0/111", "::ffff:0.0/111"}, {"::ffff:0.0.0.0/112", "::ffff:0.0/112"}, {"::ffff:0.0.0.0/113", "::ffff:0.0.0/113"}, {"::ffff:1.2.3.0/120", "::ffff:1.2.3/120"}, {"::ffff:1.2.3.0/127", "::ffff:1.2.3.0/127"}, {"::ffff:1.2.3.0/128", "::ffff:1.2.3.0/128"},
	} {
		v := networkOperatorValue(t, types[Cidr], tc.input)
		before := v
		got, err := p.Unary(context.Background(), v)
		if err != nil || got.text != tc.want || got.typ != types[Text] || v != before {
			t.Fatal(tc, got, err)
		}
	}
	for _, prefix := range []int{15, 16, 17, 31, 32, 33, 63, 64, 65, 111, 112, 113, 127, 128} {
		want := fmt.Sprintf("::/%d", prefix)
		v := networkOperatorValue(t, types[Cidr], want)
		got, err := p.Unary(context.Background(), v)
		if err != nil || got.text != want {
			t.Fatal(prefix, got, err)
		}
	}
}

func TestNetworkFormatBroadcast(t *testing.T) {
	p, types := networkFormatPlan(t, NetworkBroadcast, Inet)
	for _, tc := range []struct{ input, want string }{
		{"1.2.3.4/0", "255.255.255.255/0"}, {"1.2.3.4/1", "127.255.255.255/1"}, {"1.2.3.4/7", "1.255.255.255/7"}, {"1.2.3.4/8", "1.255.255.255/8"}, {"1.2.3.4/9", "1.127.255.255/9"}, {"1.2.3.4/31", "1.2.3.5/31"}, {"1.2.3.4/32", "1.2.3.4"}, {"192.168.0.129/25", "192.168.0.255/25"},
		{"::/0", "ffff:ffff:ffff:ffff:ffff:ffff:ffff:ffff/0"}, {"2001:db8::1/63", "2001:db8:0:1:ffff:ffff:ffff:ffff/63"}, {"2001:db8::1/64", "2001:db8::ffff:ffff:ffff:ffff/64"}, {"2001:db8::1/65", "2001:db8::7fff:ffff:ffff:ffff/65"}, {"2001:db8::1/113", "2001:db8::7fff/113"}, {"2001:db8::1/127", "2001:db8::1/127"}, {"2001:db8::2/127", "2001:db8::3/127"}, {"::1/128", "::1"}, {"::ffff:1.2.3.4/120", "::ffff:1.2.3.255/120"},
	} {
		v := networkOperatorValue(t, types[Inet], tc.input)
		before := v
		got, err := p.Unary(context.Background(), v)
		if err != nil || got.typ != types[Inet] || !got.network.valid(false) || got.network.output(false) != tc.want || got.network.prefix != v.network.prefix || got.network.family != v.network.family || v != before {
			t.Fatal(tc, got, err)
		}
	}
}

func TestNetworkFormatValidation(t *testing.T) {
	types := testTypes()
	foreign := testTypes()
	ctx := context.Background()
	for _, k := range []NetworkFormatKind{NetworkAbbrev, NetworkHost, NetworkFamily, NetworkMasklen, NetworkSameFamily, NetworkBroadcast} {
		p, _ := networkFormatPlan(t, k, Inet)
		// Use the plan's own descriptor generation rather than types from another fixture.
		v := networkOperatorValue(t, p.left, "192.168.1.5/24")
		before := v
		canceled, cancel := context.WithCancel(ctx)
		cancel()
		if k.Arity() == 1 {
			got, err := p.Unary(ctx, Null(p.left))
			if err != nil || !got.null || got.typ != p.result {
				t.Fatal(k, got, err)
			}
			if _, err := p.Unary(canceled, v); !errors.Is(err, context.Canceled) {
				t.Fatal(k, err)
			}
			if _, err := p.Unary(ctx, networkOperatorValue(t, foreign[Inet], "::1")); err == nil {
				t.Fatal("foreign input accepted")
			}
			for _, bad := range []networkDatum{{family: 0}, {family: 4, prefix: 33}, {family: 6, prefix: 129}, {family: 4, address: [16]byte{4: 1}}} {
				if _, err := p.Unary(ctx, Value{typ: p.left, network: bad}); err == nil {
					t.Fatal("corrupt input accepted", bad)
				}
			}
		} else {
			for _, pair := range [][2]Value{{Null(p.left), v}, {v, Null(p.left)}} {
				got, err := p.Binary(ctx, pair[0], pair[1])
				if err != nil || !got.null || got.typ != p.result {
					t.Fatal(got, err)
				}
			}
			if _, err := p.Binary(canceled, v, v); !errors.Is(err, context.Canceled) {
				t.Fatal(err)
			}
			if _, err := p.Binary(ctx, v, networkOperatorValue(t, foreign[Inet], "::1")); err == nil {
				t.Fatal("foreign right accepted")
			}
			bad := v
			bad.network.prefix = 33
			if _, err := p.Binary(ctx, v, bad); err == nil {
				t.Fatal("corrupt right accepted")
			}
		}
		if v != before {
			t.Fatal("validation mutated source")
		}
		right := types[Inet]
		if k.Arity() == 1 {
			right = nil
		}
		if _, err := BindNetworkFormat(k, types[Text], types[Text], right); err == nil {
			t.Fatal("invalid signature accepted", k)
		}
		if k.ResultFor(Inet) != Text {
			if _, err := BindNetworkFormat(k, types[Text], types[Inet], right); err == nil {
				t.Fatal("wrong result accepted", k)
			}
		}
	}
	if _, err := BindNetworkFormat(0, types[Text], types[Inet], nil); err == nil {
		t.Fatal("unknown operation")
	}
	if _, err := BindNetworkFormat(NetworkSameFamily, types[Bool], types[Inet], foreign[Inet]); err == nil {
		t.Fatal("mixed descriptor signature")
	}
	if _, err := BindNetworkFormat(NetworkBroadcast, foreign[Inet], types[Inet], nil); err == nil {
		t.Fatal("foreign result signature")
	}
	if _, err := (NetworkFormat{}).Unary(ctx, Null(types[Inet])); err == nil {
		t.Fatal("zero plan accepted")
	}
	p, _ := networkFormatPlan(t, NetworkAbbrev, Cidr)
	bad := networkOperatorValue(t, p.left, "10.1/16")
	bad.network.address[3] = 1
	if _, err := p.Unary(ctx, bad); err == nil {
		t.Fatal("CIDR host bits accepted")
	}
	// Nontext successful kernels operate on inline values with no heap work.
	for _, k := range []NetworkFormatKind{NetworkFamily, NetworkMasklen, NetworkSameFamily, NetworkBroadcast} {
		p, _ := networkFormatPlan(t, k, Inet)
		v := networkOperatorValue(t, p.left, "2001:db8::1/113")
		allocs := testing.AllocsPerRun(100, func() {
			var out Value
			var err error
			if k.Arity() == 1 {
				out, err = p.Unary(ctx, v)
			} else {
				out, err = p.Binary(ctx, v, v)
			}
			if err != nil || out.typ != p.result {
				panic("invalid allocation probe result")
			}
		})
		if allocs != 0 {
			t.Fatal(k, allocs)
		}
	}
}
