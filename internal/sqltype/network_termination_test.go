package sqltype

import (
	"context"
	"testing"
)

func TestNetworkIPv4Termination(t *testing.T) {
	types := testTypes()
	ctx := context.Background()
	for _, tc := range []struct{ input, output string }{
		{"1.2.3.4.", "1.2.3.4"}, {"1.2.3./24", "1.2.3.0/24"}, {"1.2.3.4./32", "1.2.3.4"}, {"1.2./16", "1.2.0.0/16"}, {"1./8", "1.0.0.0/8"},
	} {
		value, err := types[Inet].Input(ctx, tc.input, -1)
		if err != nil {
			t.Fatal(tc.input, err)
		}
		output, err := types[Inet].Output(ctx, value)
		if err != nil || output != tc.output {
			t.Fatal(tc.input, output, err)
		}
	}
	for _, input := range []string{"1.2.3.", "1.2.3./32", "1.2.3.4..", "1.2.3../24", "::ffff:1.2.3.4."} {
		if _, err := types[Inet].Input(ctx, input, -1); err == nil {
			t.Fatal("invalid INET termination", input)
		}
	}
	for _, input := range []string{"1.2.3.4.", "1.2.3./24", "::ffff:1.2.3.4."} {
		if _, err := types[Cidr].Input(ctx, input, -1); err == nil {
			t.Fatal("CIDR parser relaxed", input)
		}
	}
}
