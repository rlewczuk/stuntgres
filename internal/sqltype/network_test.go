package sqltype

import (
	"context"
	"errors"
	"fmt"
	"stuntgres/internal/sqlstate"
	"testing"
)

func TestNetworkCodecAndLiteral(t *testing.T) {
	types := testTypes()
	ctx := context.Background()
	for _, tc := range []struct {
		id             ID
		input, output  string
		family, prefix uint8
	}{
		{Inet, "192.168.0.1/24", "192.168.0.1/24", 4, 24}, {Inet, "192.168.1.5/32", "192.168.1.5", 4, 32},
		{Inet, "192.168/23", "192.168.0.0/23", 4, 23}, {Inet, "001.002.003.004/032", "1.2.3.4", 4, 32},
		{Cidr, "10", "10.0.0.0/8", 4, 8}, {Cidr, "10.1", "10.1.0.0/16", 4, 16}, {Cidr, "10.1.2", "10.1.2.0/24", 4, 24},
		{Cidr, "10.0.0.0", "10.0.0.0/32", 4, 32}, {Cidr, "128", "128.0.0.0/16", 4, 16},
		{Cidr, "192.168/25", "192.168.0.0/25", 4, 25}, {Cidr, "0xC0A8", "192.168.0.0/24", 4, 24}, {Cidr, "0xA", "160.0.0.0/16", 4, 16},
		{Cidr, "224", "224.0.0.0/4", 4, 4}, {Cidr, "224.0", "224.0.0.0/16", 4, 16}, {Cidr, "240", "240.0.0.0/32", 4, 32},
		{Inet, "2001:DB8::1/113", "2001:db8::1/113", 6, 113}, {Cidr, "::1", "::1/128", 6, 128},
		{Inet, "1:0:0:2:0:0:3:4", "1::2:0:0:3:4", 6, 128},
		{Inet, "::4.3.2.1", "::4.3.2.1", 6, 128}, {Inet, "::ffff:1.2.3.4", "::ffff:1.2.3.4", 6, 128},
		{Inet, "::", "::", 6, 128}, {Cidr, "::/0", "::/0", 6, 0}, {Inet, "0.0.0.0/0", "0.0.0.0/0", 4, 0},
	} {
		t.Run(fmt.Sprintf("%d_%s", tc.id, tc.input), func(t *testing.T) {
			d := types[tc.id]
			v, err := d.Input(ctx, tc.input, -1)
			if err != nil {
				t.Fatal(err)
			}
			if v.network.family != tc.family || v.network.prefix != tc.prefix || !v.network.valid(tc.id == Cidr) {
				t.Fatal(v.network)
			}
			out, err := d.Output(ctx, v)
			if err != nil || out != tc.output {
				t.Fatal(out, err, tc.output)
			}
			round, err := d.Input(ctx, out, -1)
			if err != nil || !SameDatum(v, round) {
				t.Fatal(round, err)
			}
			literal, err := d.SQLLiteral(ctx, v)
			if err != nil || literal != d.SQLName()+" '"+tc.output+"'" {
				t.Fatal(literal, err)
			}
			next, err := v.Retag(testTypes()[tc.id])
			if err != nil || next.network != v.network {
				t.Fatal(next, err)
			}
			if _, err := v.Retag(types[Text]); err == nil {
				t.Fatal("foreign retag")
			}
		})
	}
	canceled, cancel := context.WithCancel(ctx)
	cancel()
	if _, err := types[Inet].Input(canceled, "::1", -1); !errors.Is(err, context.Canceled) {
		t.Fatal(err)
	}
	if _, err := types[Inet].Output(ctx, Value{typ: types[Inet], network: networkDatum{family: 4, prefix: 33}}); err == nil {
		t.Fatal("invalid prefix output")
	}
}

func TestNetworkDiagnosticsAndHostBits(t *testing.T) {
	types := testTypes()
	ctx := context.Background()
	for _, id := range []ID{Inet, Cidr} {
		for _, input := range []string{"", "bad", "256.1.2.3", "1.2.3.4.5", "1.2.3.4/33", "::/129", "::/0128", "1::2::3", "10000::", "[::1]", "fe80::1%eth0", "1.2.3.4 ", " 1.2.3.4", "::ffff:01.2.3.4", "::/", "1.2.3.4/-1", "1.2.3.4/255.255.255.0"} {
			_, err := types[id].Input(ctx, input, -1)
			var d *sqlstate.Error
			if !errors.As(err, &d) || d.Code != "22P02" || d.Message != fmt.Sprintf("invalid input syntax for type %s: \"%s\"", types[id].Name(), input) || d.Detail != "" || d.Position != "" {
				t.Fatal(input, err)
			}
		}
	}
	for _, input := range []string{"192.168.0.1/24", "2001:db8::1/113", "1.2.3.4/0"} {
		if _, err := types[Inet].Input(ctx, input, -1); err != nil {
			t.Fatal(err)
		}
		_, err := types[Cidr].Input(ctx, input, -1)
		var d *sqlstate.Error
		if !errors.As(err, &d) || d.Code != "22P02" || d.Message != fmt.Sprintf("invalid cidr value: \"%s\"", input) || d.Detail != "Value has bits set to right of mask." {
			t.Fatal(err)
		}
	}
	for _, input := range []string{"192.168", "192.168/24", "0xC0A8"} {
		if _, err := types[Inet].Input(ctx, input, -1); err == nil {
			t.Fatal("inet shorthand boundary", input)
		}
	}
}

func TestNetworkCoercionAndIdentity(t *testing.T) {
	types := testTypes()
	ctx := context.Background()
	for _, tc := range []struct{ input, output string }{{"192.168.0.129/25", "192.168.0.128/25"}, {"2001:db8::ffff/113", "2001:db8::8000/113"}, {"1.2.3.4/0", "0.0.0.0/0"}, {"2001:db8::1/0", "::/0"}} {
		source, err := types[Inet].Input(ctx, tc.input, -1)
		if err != nil {
			t.Fatal(err)
		}
		for _, mode := range []CoercionContext{Assignment, Explicit} {
			plan, err := PlanCoercion(types[Inet].Usage(), types[Cidr].Usage(), mode)
			if err != nil {
				t.Fatal(err)
			}
			value, err := plan.Apply(ctx, source, InputEnvironment{}, OutputEnvironment{})
			if err != nil {
				t.Fatal(err)
			}
			output, err := types[Cidr].Output(ctx, value)
			if err != nil || output != tc.output {
				t.Fatal(output, err)
			}
			null, err := plan.Apply(ctx, Null(types[Inet]), InputEnvironment{}, OutputEnvironment{})
			if err != nil || !null.IsNull() || null.Type() != types[Cidr] {
				t.Fatal(null, err)
			}
			if SameDatum(source, value) {
				t.Fatal("different logical types")
			}
		}
	}
	if _, err := PlanCoercion(types[Inet].Usage(), types[Cidr].Usage(), Implicit); err == nil {
		t.Fatal("implicit inet to cidr")
	}
	cidr, err := types[Cidr].Input(ctx, "10", -1)
	if err != nil {
		t.Fatal(err)
	}
	implicit, err := PlanCoercion(types[Cidr].Usage(), types[Inet].Usage(), Implicit)
	if err != nil {
		t.Fatal(err)
	}
	if implicit.EvaluationFunctionCount() != 0 {
		t.Fatal("cidr to inet is a binary relabel")
	}
	inet, err := implicit.Apply(ctx, cidr, InputEnvironment{}, OutputEnvironment{})
	if err != nil || inet.network != cidr.network || inet.Type() != types[Inet] {
		t.Fatal(inet, err)
	}
	for _, id := range []ID{Inet, Cidr} {
		for _, mode := range []CoercionContext{Implicit, Assignment} {
			if _, err := PlanCoercion(types[Text].Usage(), types[id].Usage(), mode); err == nil {
				t.Fatal("implicit text input", id, mode)
			}
		}
		d := types[id]
		v, err := d.Input(ctx, "192.168.1.5", -1)
		if err != nil {
			t.Fatal(err)
		}
		text, err := PlanCoercion(d.Usage(), types[Text].Usage(), Explicit)
		if err != nil {
			t.Fatal(err)
		}
		rendered, err := text.Apply(ctx, v, InputEnvironment{}, OutputEnvironment{})
		if err != nil || rendered.text != "192.168.1.5/32" {
			t.Fatal(rendered, err)
		}
		same, err := d.Input(ctx, "192.168.1.5/32", -1)
		if err != nil || !SameDatum(v, same) || !SameDatum(Null(d), Null(d)) || SameDatum(v, Null(d)) {
			t.Fatal("datum identity", err)
		}
		literal, err := d.SQLLiteral(ctx, Null(d))
		if err != nil || literal != "CAST(NULL AS "+d.SQLName()+")" {
			t.Fatal(literal, err)
		}
		domain, err := Domain(60000, "n", "public.n", d.Usage())
		if err != nil {
			t.Fatal(err)
		}
		wrapped, err := domain.Wrap(v)
		if err != nil {
			t.Fatal(err)
		}
		out, err := domain.Output(ctx, wrapped)
		raw, _ := d.Output(ctx, v)
		if err != nil || out != raw || !SameDatum(wrapped, wrapped) {
			t.Fatal(out, err)
		}
	}
}

func TestNetworkOrderingAndCapabilities(t *testing.T) {
	types := testTypes()
	ctx := context.Background()
	d := types[Inet]
	inputs := []string{"1.2.3.4/0", "1.2.3.5/0", "10.255.255.255/8", "10.0.0.0/16", "10.0.0.1/16", "255.255.255.255", "::1", "::4.3.2.1", "::10.2.3.4", "::ffff:1.2.3.4"}
	values := make([]Value, len(inputs))
	for i, s := range inputs {
		var err error
		values[i], err = d.Input(ctx, s, -1)
		if err != nil {
			t.Fatal(err)
		}
	}
	comparison, err := BindOrdering(d.Usage())
	if err != nil {
		t.Fatal(err)
	}
	for i, a := range values {
		for j, b := range values {
			got, err := comparison.Compare(ctx, a, b, InputEnvironment{})
			want := 0
			if i < j {
				want = -1
			} else if i > j {
				want = 1
			}
			if err != nil || got != want {
				t.Fatal(inputs[i], inputs[j], got, want, err)
			}
			for k, c := range values {
				if i < j && j < k {
					ab := compareNetwork(a.network, b.network)
					bc := compareNetwork(b.network, c.network)
					ac := compareNetwork(a.network, c.network)
					if ab >= 0 || bc >= 0 || ac >= 0 {
						t.Fatal("transitivity")
					}
				}
			}
		}
	}
	for _, id := range []ID{Inet, Cidr} {
		scalar := types[id]
		if !scalar.Concrete() || !StoredScalarCastType(id) || scalar.Usage().Collation.ID != 0 {
			t.Fatal(scalar)
		}
		array := types[scalar.AssociatedArray()]
		if array == nil || array.Concrete() || array.Element() != scalar || !array.IsArray() {
			t.Fatal("lookup-only array", array)
		}
		if _, err := Array(60000, "_n", scalar); err == nil {
			t.Fatal("network array constructor admitted")
		}
		domain, err := Domain(60001, "n", "public.n", scalar.Usage())
		if err != nil {
			t.Fatal(err)
		}
		if _, err := Array(60002, "_n", domain); err == nil {
			t.Fatal("network domain array constructor admitted")
		}
		if LookupArray(60002, "_n", domain).Concrete() {
			t.Fatal("network domain associated array became concrete")
		}
		class, ok := DefaultIndexClass(scalar)
		if !ok || class.Name != "inet_ops" || class.Family != "network_ops" || class.OID() != 10015 || class.input != Inet || !class.Accepts(scalar) {
			t.Fatal(class, ok)
		}
		if _, err := BindGrouping(scalar.Usage()); err != nil {
			t.Fatal("network grouping unavailable", err)
		}
		signature, err := ResolveEquality(types[id], types[Cidr])
		if err != nil || signature != (EqualitySignature{Inet, Inet}) {
			t.Fatal(signature, err)
		}
	}
	a, _ := d.Input(ctx, "10.0.0.1/8", -1)
	b, _ := d.Input(ctx, "10.0.0.1/16", -1)
	if SameDatum(a, b) || compareNetwork(a.network, b.network) >= 0 {
		t.Fatal("prefix identity")
	}
	mapped, _ := d.Input(ctx, "::ffff:1.2.3.4", -1)
	ipv4, _ := d.Input(ctx, "1.2.3.4", -1)
	if mapped.network.family != 6 || mapped.network == ipv4.network {
		t.Fatal("mapped family collapsed")
	}
}
