package reference_test

import (
	"fmt"
	"testing"
)

func TestReferenceINETIPv6MaskTermination(t *testing.T) {
	for _, typ := range []string{"inet", "cidr"} {
		for i, tc := range []struct{ input, output string }{
			{"::ffff:1.2.3./128", "::ffff:1.2.3.0"},
			{"1:2:3:4:5:6:7:8:/128", "1:2:3:4:5:6:7:8"},
			{"::ffff:1.2.3.4/128", "::ffff:1.2.3.4"},
			{"1:2:3:4:5:6:7:8/128", "1:2:3:4:5:6:7:8"},
			{"::ffff:1.2.3.", ""},
			{"1:2:3:4:5:6:7:8:", ""},
			{"::ffff:1.2.3.4./128", ""},
			{"::ffff:1.2.3.4.", ""},
			{"::ffff:1.2.3.4.5/128", ""},
			{"1:2:3:4:5:6:7:8:9/128", ""},
			{"1:2:3:4:5:6:7:8:9:/128", ""},
			{"::ffff:1.2.3./129", ""},
			{"::ffff:1.2.3./", ""},
			{"::ffff:1.2.3./x", ""},
			{"::ffff:1.2.3./128/1", ""},
			{"1:2:3:4:5:6:7:8:/129", ""},
			{"1:2:3:4:5:6:7:8:/", ""},
			{"1:2:3:4:5:6:7:8:/x", ""},
			{"1:2:3:4:5:6:7:8:/128/1", ""},
		} {
			t.Run(fmt.Sprintf("%s_%02d", typ, i), func(t *testing.T) {
				output := tc.output
				if output != "" && typ == "cidr" {
					output += "/128"
				}
				inetTerminationCase(t, typ, tc.input, output)
			})
		}
	}
}
