package reference_test

import (
	"fmt"
	"stuntgres/reference/fixture"
	"testing"
)

// Manual examples and released-source hypotheses, NOT observed native goldens.
// In particular odd_zero_run intentionally asserts the source-trace hypothesis;
// root must record pinned18.6 failure/output before correcting any expectation.
func TestReferenceINETFormatAbbreviation(t *testing.T) {
	vectors := []struct{ input, abbrev, inet, host string }{
		{"10", "10/8", "10.0.0.0/8", "10.0.0.0"}, {"10.1", "10.1/16", "10.1.0.0/16", "10.1.0.0"}, {"128", "128.0/16", "128.0.0.0/16", "128.0.0.0"},
		{"192.168/24", "192.168.0/24", "192.168.0.0/24", "192.168.0.0"}, {"192.168/25", "192.168.0.0/25", "192.168.0.0/25", "192.168.0.0"},
		{"192.168.100.128/25", "192.168.100.128/25", "192.168.100.128/25", "192.168.100.128"}, {"10.1.2.3/32", "10.1.2.3/32", "10.1.2.3", "10.1.2.3"},
		{"2001:4f8:3:ba::/64", "2001:4f8:3:ba/64", "2001:4f8:3:ba::/64", "2001:4f8:3:ba::"},
		{"::ffff:1.2.3.0/120", "::ffff:1.2.3/120", "::ffff:1.2.3.0/120", "::ffff:1.2.3.0"}, {"::ffff:1.2.3.0/128", "::ffff:1.2.3.0/128", "::ffff:1.2.3.0", "::ffff:1.2.3.0"},
		{"0.0.0.0/0", "0/0", "0.0.0.0/0", "0.0.0.0"}, {"::/0", "::/0", "::/0", "::"}, {"2001:db8::/32", "2001:db8/32", "2001:db8::/32", "2001:db8::"}, {"2001::/16", "2001::/16", "2001::/16", "2001::"}, {"2001:0:1::/48", "2001::1/48", "2001:0:1::/48", "2001:0:1::"},
		{"2001:db8:0:1:0:2::/112", "2001:db8:0:1::0/112", "2001:db8:0:1:0:2::/112", "2001:db8:0:1:0:2::"},
		{"2001:0:0:1::/64", "2001::1/64", "2001:0:0:1::/64", "2001:0:0:1::"},
	}
	for i, tc := range vectors {
		t.Run(fmt.Sprintf("vector_%02d", i), func(t *testing.T) {
			inetFormatScenario(t, func(p *fixture.Pair, b fixture.Backend) {
				// text(cidr) promotes to INET, always includes its prefix.
				text := tc.inet
				if tc.input == "10.1.2.3/32" {
					text = "10.1.2.3/32"
				}
				if tc.input == "::ffff:1.2.3.0/128" {
					text = "::ffff:1.2.3.0/128"
				}
				inetFormatBoth(t, p, b, fixture.Select("SELECT abbrev($1::cidr) AS a,abbrev(($1::cidr)::inet) AS i,host($1::cidr) AS h,text($1::cidr) AS t", []string{"a", "i", "h", "t"}, []string{"TEXT", "TEXT", "TEXT", "TEXT"}, [][]any{{tc.abbrev, tc.inet, tc.host, text}}, tc.input))
			})
		})
	}
	// Zero networks isolate every released truncated-word boundary without host bits.
	for _, prefix := range []int{15, 16, 17, 31, 32, 33, 63, 64, 65, 111, 112, 113, 127, 128} {
		t.Run(fmt.Sprintf("zero_prefix_%d", prefix), func(t *testing.T) {
			inetFormatScenario(t, func(p *fixture.Pair, b fixture.Backend) {
				want := fmt.Sprintf("::/%d", prefix)
				inetFormatBoth(t, p, b, fixture.Select("SELECT abbrev($1::cidr) AS a", []string{"a"}, []string{"TEXT"}, [][]any{{want}}, want))
			})
		})
	}
	for _, tc := range []struct{ in, out string }{
		{"::ffff:0.0.0.0/111", "::ffff:0.0/111"}, {"::ffff:0.0.0.0/112", "::ffff:0.0/112"}, {"::ffff:0.0.0.0/113", "::ffff:0.0.0/113"}, {"::ffff:1.2.3.0/127", "::ffff:1.2.3.0/127"},
	} {
		t.Run("mapped_"+tc.in, func(t *testing.T) {
			inetFormatScenario(t, func(p *fixture.Pair, b fixture.Backend) {
				inetFormatBoth(t, p, b, fixture.Select("SELECT abbrev($1::cidr) AS a", []string{"a"}, []string{"TEXT"}, [][]any{{tc.out}}, tc.in))
			})
		})
	}
}
