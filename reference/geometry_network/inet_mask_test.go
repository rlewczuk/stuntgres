package reference_test

import (
	"fmt"
	"stuntgres/reference/fixture"
	"testing"
)

// inetMaskScenario runs the same definitions on every available backend.
func inetMaskScenario(t *testing.T, body func(*fixture.Pair, fixture.Backend)) {
	t.Helper()
	p := fixture.NewScenario(t)
	for _, b := range p.Backends() {
		body(p, b)
	}
}

var inetMaskNames = []string{"hm", "nm", "n", "si", "sc", "m"}
var inetMaskTypes = []string{"INET", "INET", "CIDR", "INET", "CIDR", "CIDR"}

const inetMaskManual = "SELECT hostmask(INET '192.168.23.20/30') AS hm,netmask(INET '192.168.1.5/24') AS nm,network(INET '192.168.1.5/24') AS n,set_masklen(INET '192.168.1.5/24',16) AS si,set_masklen(CIDR '192.168.1.0/24',16) AS sc,inet_merge(INET '192.168.1.5/24',INET '192.168.2.5/24') AS m"

var inetMaskManualRows = [][]any{{"0.0.0.3", "255.255.255.0", "192.168.1.0/24", "192.168.1.5/16", "192.168.0.0/16", "192.168.0.0/22"}}

func TestReferenceINETMaskValues(t *testing.T) {
	inetMaskScenario(t, func(p *fixture.Pair, b fixture.Backend) {
		inetFormatBoth(t, p, b, fixture.Select(inetMaskManual, inetMaskNames, inetMaskTypes, inetMaskManualRows))
		inetFormatBoth(t, p, b, fixture.Select("SELECT hostmask(INET '10:23::f1/64') AS hm,netmask(INET '10:23::f1/64') AS nm,network(INET '10:23::f1/64') AS n,set_masklen(INET '10:23::f1/64',24) AS si,set_masklen(CIDR '10:23::f1/128',24) AS sc,inet_merge(INET '::1',INET '::2') AS m", inetMaskNames, inetMaskTypes, [][]any{{"::ffff:ffff:ffff:ffff", "ffff:ffff:ffff:ffff::", "10:23::/64", "10:23::f1/24", "10::/24", "::/126"}}))
		for _, tc := range []struct{ in, out string }{{"192.168.1.255/25", "192.168.1.128/25"}, {"192.168.1.226", "192.168.1.226/32"}} {
			inetFormatBoth(t, p, b, fixture.Select("SELECT network($1::inet) AS n", []string{"n"}, []string{"CIDR"}, [][]any{{tc.out}}, tc.in))
		}
	})
}

func TestReferenceINETMaskBoundaries(t *testing.T) {
	// Literal independent bit-boundary expectations; canonical IPv6 spellings are
	// source-derived hypotheses pending the pinned oracle, never native goldens.
	for _, tc := range []struct{ ip, hm, nm, n string }{
		{"1.2.3.4/0", "255.255.255.255", "0.0.0.0", "0.0.0.0/0"},
		{"1.2.3.4/1", "127.255.255.255", "128.0.0.0", "0.0.0.0/1"},
		{"1.2.3.4/7", "1.255.255.255", "254.0.0.0", "0.0.0.0/7"},
		{"1.2.3.4/8", "0.255.255.255", "255.0.0.0", "1.0.0.0/8"},
		{"1.2.3.4/9", "0.127.255.255", "255.128.0.0", "1.0.0.0/9"},
		{"1.2.3.4/24", "0.0.0.255", "255.255.255.0", "1.2.3.0/24"},
		{"1.2.3.4/25", "0.0.0.127", "255.255.255.128", "1.2.3.0/25"},
		{"1.2.3.4/31", "0.0.0.1", "255.255.255.254", "1.2.3.4/31"},
		{"1.2.3.4/32", "0.0.0.0", "255.255.255.255", "1.2.3.4/32"},
		{"2001:db8::ffff/0", "ffff:ffff:ffff:ffff:ffff:ffff:ffff:ffff", "::", "::/0"},
		{"2001:db8::ffff/1", "7fff:ffff:ffff:ffff:ffff:ffff:ffff:ffff", "8000::", "::/1"},
		{"2001:db8::ffff/63", "::1:ffff:ffff:ffff:ffff", "ffff:ffff:ffff:fffe::", "2001:db8::/63"},
		{"2001:db8::ffff/64", "::ffff:ffff:ffff:ffff", "ffff:ffff:ffff:ffff::", "2001:db8::/64"},
		{"2001:db8::ffff/65", "::7fff:ffff:ffff:ffff", "ffff:ffff:ffff:ffff:8000::", "2001:db8::/65"},
		{"2001:db8::ffff/95", "::1:ffff:ffff", "ffff:ffff:ffff:ffff:ffff:fffe::", "2001:db8::/95"},
		{"2001:db8::ffff/96", "::255.255.255.255", "ffff:ffff:ffff:ffff:ffff:ffff::", "2001:db8::/96"},
		{"2001:db8::ffff/97", "::127.255.255.255", "ffff:ffff:ffff:ffff:ffff:ffff:8000:0", "2001:db8::/97"},
		{"2001:db8::ffff/111", "::0.1.255.255", "ffff:ffff:ffff:ffff:ffff:ffff:fffe:0", "2001:db8::/111"},
		{"2001:db8::ffff/112", "::ffff", "ffff:ffff:ffff:ffff:ffff:ffff:ffff:0", "2001:db8::/112"},
		{"2001:db8::ffff/113", "::7fff", "ffff:ffff:ffff:ffff:ffff:ffff:ffff:8000", "2001:db8::8000/113"},
		{"2001:db8::ffff/127", "::1", "ffff:ffff:ffff:ffff:ffff:ffff:ffff:fffe", "2001:db8::fffe/127"},
		{"2001:db8::ffff/128", "::", "ffff:ffff:ffff:ffff:ffff:ffff:ffff:ffff", "2001:db8::ffff/128"},
		{"::ffff:1.2.3.4/120", "::ff", "ffff:ffff:ffff:ffff:ffff:ffff:ffff:ff00", "::ffff:1.2.3.0/120"},
	} {
		t.Run(tc.ip, func(t *testing.T) {
			inetMaskScenario(t, func(p *fixture.Pair, b fixture.Backend) {
				inetFormatBoth(t, p, b, fixture.Select("SELECT hostmask($1::inet) AS hm,netmask($1::inet) AS nm,network($1::inet) AS n", inetMaskNames[:3], inetMaskTypes[:3], [][]any{{tc.hm, tc.nm, tc.n}}, tc.ip))
			})
		})
	}
	inetMaskScenario(t, func(p *fixture.Pair, b fixture.Backend) {
		for _, tc := range []struct {
			ip, net        string
			width          int
			zero, full, cf string
		}{
			{"192.168.1.5/24", "192.168.1.0/24", 32, "0.0.0.0/0", "192.168.1.5", "192.168.1.0/32"},
			{"2001:db8::ffff/113", "2001:db8::8000/113", 128, "::/0", "2001:db8::ffff", "2001:db8::8000/128"},
		} {
			for _, n := range []int{0, tc.width, -1} {
				si, sc := tc.full, tc.cf
				if n == 0 {
					si = tc.full + "/0"
					sc = tc.zero
				}
				inetFormatBoth(t, p, b, fixture.Select("SELECT set_masklen($1::inet,$3::integer) AS si,set_masklen($2::cidr,$3::integer) AS sc", []string{"si", "sc"}, []string{"INET", "CIDR"}, [][]any{{si, sc}}, tc.ip, tc.net, n))
			}
		}
		inetFormatBoth(t, p, b, fixture.Select("SELECT set_masklen(set_masklen(INET '192.168.1.5/24',16),32) AS si,set_masklen(set_masklen(CIDR '192.168.1.0/24',16),32) AS sc", []string{"si", "sc"}, []string{"INET", "CIDR"}, [][]any{{"192.168.1.5", "192.168.0.0/32"}}))
		for _, tc := range []struct{ a, b, w string }{
			{"10.0.0.0/8", "11.1.2.3/8", "10.0.0.0/7"}, {"10.0.0.0/8", "9.1.2.3/8", "8.0.0.0/6"},
			{"192.168.1.5/24", "192.168.1.5/16", "192.168.0.0/16"}, {"192.168.1.5", "192.168.1.5", "192.168.1.5/32"},
			{"::1", "::1", "::1/128"}, {"::1", "::2", "::/126"}, {"::1/0", "ffff::/128", "::/0"},
			{"10.1.2.3/8", "10.9.8.7/24", "10.0.0.0/8"}, {"10.1.2.3/8", "10.1.2.3/8", "10.0.0.0/8"},
		} {
			for _, pair := range [][2]string{{tc.a, tc.b}, {tc.b, tc.a}} {
				inetFormatBoth(t, p, b, fixture.Select("SELECT inet_merge($1::inet,$2::inet) AS m", []string{"m"}, []string{"CIDR"}, [][]any{{tc.w}}, pair[0], pair[1]))
			}
		}
	})
}

func TestReferenceINETMaskRoutineIdentity(t *testing.T) {
	inetMaskScenario(t, func(p *fixture.Pair, b fixture.Backend) {
		for _, tc := range []struct {
			name, sig, args    string
			oid, result, arity int64
		}{
			{"hostmask", "inet", "869", 1362, 869, 1}, {"netmask", "inet", "869", 696, 869, 1}, {"network", "inet", "869", 683, 650, 1},
			{"inet_merge", "inet,inet", "869 869", 4063, 650, 2}, {"set_masklen", "inet,integer", "869 23", 605, 869, 2}, {"set_masklen", "cidr,integer", "650 23", 635, 650, 2},
		} {
			t.Run(fmt.Sprint(tc.oid), func(t *testing.T) {
				inetFormatBoth(t, p, b, fixture.Select("SELECT oid::regprocedure::text AS identity,proname,pronamespace::bigint AS namespace,pronargs,prorettype::bigint AS result,proisstrict,provolatile,proparallel FROM pg_proc WHERE oid=$1", []string{"identity", "proname", "namespace", "pronargs", "result", "proisstrict", "provolatile", "proparallel"}, []string{"TEXT", "NAME", "INT8", "INT2", "INT8", "BOOL", "CHAR", "CHAR"}, [][]any{{tc.name + "(" + tc.sig + ")", tc.name, int64(11), tc.arity, tc.result, true, "i", "s"}}, tc.oid))
				if b.PG {
					// These additional pg_proc columns are not a native catalog capability.
					inetFormatBoth(t, p, b, fixture.Select("SELECT proargtypes::text AS args,prokind,proretset,provariadic::bigint AS variadic,pronargdefaults,proargnames IS NULL AS unnamed FROM pg_proc WHERE oid=$1", []string{"args", "prokind", "proretset", "variadic", "pronargdefaults", "unnamed"}, []string{"TEXT", "CHAR", "BOOL", "INT8", "INT2", "BOOL"}, [][]any{{tc.args, "f", false, int64(0), int64(0), true}}, tc.oid))
				}
			})
		}
	})
}
