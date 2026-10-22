package sqltype

import (
	"context"
	"errors"
	"testing"
)

func TestNetworkSubnetValues(t *testing.T) {
	types := testTypes()
	inet, boolean := types[Inet], types[Bool]
	for _, tc := range []struct {
		a, b string
		want [5]bool
	}{
		{"192.168.1.5", "192.168.1.199/24", [5]bool{true, true, false, false, true}},
		{"192.168.1.1/24", "192.168.1.254/24", [5]bool{false, true, false, true, true}},
		{"192.168.1.255/25", "192.168.1.1/25", [5]bool{}},
		{"2001:db8::ffff/114", "2001:db8::8001/113", [5]bool{true, true, false, false, true}},
		{"::ffff:1.2.3.4", "1.2.3.4", [5]bool{}},
	} {
		pair := [2]Value{networkOperatorValue(t, inet, tc.a), networkOperatorValue(t, inet, tc.b)}
		before := pair
		for i, want := range tc.want {
			p, err := BindNetworkSubnet(NetworkSubnetOperator(i), boolean.Usage(), inet.Usage(), inet.Usage())
			if err != nil {
				t.Fatal(err)
			}
			got, err := p.Evaluate(context.Background(), pair)
			if err != nil || got.typ != boolean || got.null || got.boolean != want {
				t.Fatal(tc, i, got, err)
			}
		}
		if pair != before {
			t.Fatal("mutated inputs")
		}
	}
}
func TestNetworkSubnetBoundaries(t *testing.T) {
	types := testTypes()
	inet, boolean := types[Inet], types[Bool]
	for _, family := range []uint8{4, 6} {
		width := 32
		if family == 6 {
			width = 128
		}
		for prefix := range width + 1 {
			a := Value{typ: inet, network: networkDatum{family: family, prefix: uint8(prefix)}}
			for _, op := range []NetworkSubnetOperator{NetworkSubnetSub, NetworkSubnetSubeq, NetworkSubnetSup, NetworkSubnetSupeq, NetworkSubnetOverlap} {
				p, err := BindNetworkSubnet(op, boolean.Usage(), inet.Usage(), inet.Usage())
				if err != nil {
					t.Fatal(err)
				}
				b := a
				// Changing the first host bit leaves equal-prefix inclusion unchanged.
				if prefix < width {
					b.network.address[prefix/8] ^= 1 << uint(7-prefix%8)
				}
				want := op == NetworkSubnetSubeq || op == NetworkSubnetSupeq || op == NetworkSubnetOverlap
				got, err := p.Evaluate(context.Background(), [2]Value{a, b})
				if err != nil || got.boolean != want {
					t.Fatal(family, prefix, op, got, err)
				}
				if prefix > 0 {
					b = a
					b.network.address[(prefix-1)/8] ^= 1 << uint(7-(prefix-1)%8)
					got, err = p.Evaluate(context.Background(), [2]Value{a, b})
					if err != nil || got.boolean {
						t.Fatal("significant bit", family, prefix, op, got, err)
					}
				}
			}
		}
	}
}
func TestNetworkSubnetPlanValidation(t *testing.T) {
	types, foreign := testTypes(), testTypes()
	inet, boolean := types[Inet], types[Bool]
	for _, sig := range [][3]Usage{{foreign[Bool].Usage(), inet.Usage(), inet.Usage()}, {boolean.Usage(), inet.Usage(), foreign[Inet].Usage()}, {boolean.Usage(), types[Cidr].Usage(), types[Cidr].Usage()}, {inet.Usage(), inet.Usage(), inet.Usage()}} {
		if _, err := BindNetworkSubnet(NetworkSubnetSub, sig[0], sig[1], sig[2]); err == nil {
			t.Fatal("invalid authority accepted")
		}
	}
	if _, err := BindNetworkSubnet(255, boolean.Usage(), inet.Usage(), inet.Usage()); err == nil {
		t.Fatal("invalid operation")
	}
	p, err := BindNetworkSubnet(NetworkSubnetSub, boolean.Usage(), inet.Usage(), inet.Usage())
	if err != nil {
		t.Fatal(err)
	}
	pair := [2]Value{Null(inet), Null(inet)}
	v, err := p.Evaluate(context.Background(), pair)
	if err != nil || !v.null || v.typ != boolean {
		t.Fatal(v, err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err = p.Evaluate(ctx, pair); !errors.Is(err, context.Canceled) {
		t.Fatal(err)
	}
	pair[0] = Null(foreign[Inet])
	if _, err = p.Evaluate(context.Background(), pair); err == nil {
		t.Fatal("foreign value")
	}
	pair = [2]Value{{typ: inet}, {typ: inet}}
	if _, err = p.Evaluate(context.Background(), pair); err == nil {
		t.Fatal("invalid datum")
	}
}
