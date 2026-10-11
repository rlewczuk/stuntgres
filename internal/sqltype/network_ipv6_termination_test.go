package sqltype

import (
	"context"
	"testing"
)

func TestNetworkIPv6MaskTermination(t *testing.T) {
	types := testTypes()
	ctx := context.Background()
	for _, id := range []ID{Inet, Cidr} {
		for _, tc := range []struct{ input, output string }{
			{"::ffff:1.2.3./128", "::ffff:1.2.3.0"},
			{"1:2:3:4:5:6:7:8:/128", "1:2:3:4:5:6:7:8"},
			{"::ffff:1.2.3.4/128", "::ffff:1.2.3.4"},
			{"1:2:3:4:5:6:7:8/128", "1:2:3:4:5:6:7:8"},
		} {
			t.Run(types[id].SQLName()+"_"+tc.input, func(t *testing.T) {
				value, err := types[id].Input(ctx, tc.input, -1)
				if err != nil {
					t.Fatal(err)
				}
				want := tc.output
				if id == Cidr {
					want += "/128"
				}
				output, err := types[id].Output(ctx, value)
				if err != nil || output != want {
					t.Fatal(output, want, err)
				}
			})
		}
		for _, input := range []string{
			"::ffff:1.2.3.", "1:2:3:4:5:6:7:8:",
			"::ffff:1.2.3.4./128", "::ffff:1.2.3.4.", "::ffff:1.2.3.4.5/128",
			"1:2:3:4:5:6:7:8:9/128", "1:2:3:4:5:6:7:8:9:/128",
			"::ffff:1.2.3./129", "::ffff:1.2.3./", "::ffff:1.2.3./x", "::ffff:1.2.3./128/1",
			"1:2:3:4:5:6:7:8:/129", "1:2:3:4:5:6:7:8:/", "1:2:3:4:5:6:7:8:/x", "1:2:3:4:5:6:7:8:/128/1",
		} {
			t.Run(types[id].SQLName()+"_"+input, func(t *testing.T) {
				if _, err := types[id].Input(ctx, input, -1); err == nil {
					t.Fatal("invalid IPv6 termination", input)
				}
			})
		}
	}
}
