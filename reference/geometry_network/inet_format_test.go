package reference_test

import (
	"fmt"
	"stuntgres/reference/fixture"
	"testing"
)

func inetFormatScenario(t *testing.T, body func(*fixture.Pair, fixture.Backend)) {
	t.Helper()
	p := fixture.NewScenario(t)
	for _, b := range p.Backends() {
		body(p, b)
	}
}

func inetFormatBoth(t *testing.T, p *fixture.Pair, b fixture.Backend, s fixture.Step) {
	t.Helper()
	fixture.RunStep(t, p.Ctx, b.DB, b.Recorder, s)
	fixture.RunStep(t, p.Ctx, b.DB, b.Recorder, fixture.Prepared(s))
}

func inetFormatValueStep(q string, rows [][]any, args ...any) fixture.Step {
	return fixture.Select(q, []string{"a", "h", "t", "f", "m", "b", "s"}, []string{"TEXT", "TEXT", "TEXT", "INT4", "INT4", "INET", "BOOL"}, rows, args...)
}

const inetFormatProjection = "abbrev(ip) AS a,host(ip) AS h,text(ip) AS t,family(ip) AS f,masklen(ip) AS m,broadcast(ip) AS b,inet_same_family(ip,INET '::ffff:1.2.3.4') AS s"

func TestReferenceINETFormatValues(t *testing.T) {
	inetFormatScenario(t, func(p *fixture.Pair, b fixture.Backend) {
		run := func(s fixture.Step) { fixture.RunStep(t, p.Ctx, b.DB, b.Recorder, s) }
		run(fixture.Exec("CREATE TABLE nf_values(id integer,ip inet)", 0))
		run(fixture.Exec("INSERT INTO nf_values VALUES(1,'192.168.1.5/24'),(2,'10.1.0.0/32'),(3,'2001:0DB8:0:0:0:0:0:1/128'),(4,'::ffff:1.2.3.4/120'),(5,NULL)", 5))
		inetFormatBoth(t, p, b, inetFormatValueStep("SELECT "+inetFormatProjection+" FROM nf_values ORDER BY id", [][]any{
			{"192.168.1.5/24", "192.168.1.5", "192.168.1.5/24", int64(4), int64(24), "192.168.1.255/24", false},
			{"10.1.0.0", "10.1.0.0", "10.1.0.0/32", int64(4), int64(32), "10.1.0.0", false},
			{"2001:db8::1", "2001:db8::1", "2001:db8::1/128", int64(6), int64(128), "2001:db8::1", true},
			{"::ffff:1.2.3.4/120", "::ffff:1.2.3.4", "::ffff:1.2.3.4/120", int64(6), int64(120), "::ffff:1.2.3.255/120", true},
			{nil, nil, nil, nil, nil, nil, nil},
		}))
		inetFormatBoth(t, p, b, fixture.Select("SELECT abbrev(NULL::cidr) AS a,inet_same_family(NULL::inet,INET '::1') AS l,inet_same_family(INET '1.2.3.4',NULL::inet) AS r,inet_same_family(INET '1.2.3.4/8',INET '9.8.7.6/32') AS s", []string{"a", "l", "r", "s"}, []string{"TEXT", "BOOL", "BOOL", "BOOL"}, [][]any{{nil, nil, nil, true}}))
		run(fixture.Exec("CREATE DOMAIN nf_inet AS inet", 0))
		run(fixture.Exec("CREATE DOMAIN nf_cidr AS cidr", 0))
		inetFormatBoth(t, p, b, fixture.Select("SELECT host('192.168.1.5/24'::nf_inet) AS h,abbrev('10.1'::nf_cidr) AS a,family('10.1'::nf_cidr) AS f", []string{"h", "a", "f"}, []string{"TEXT", "TEXT", "INT4"}, [][]any{{"192.168.1.5", "10.1/16", int64(4)}}))
	})
}

func TestReferenceINETFormatBroadcast(t *testing.T) {
	for _, tc := range []struct{ in, out string }{
		{"1.2.3.4/0", "255.255.255.255/0"}, {"1.2.3.4/1", "127.255.255.255/1"}, {"1.2.3.4/7", "1.255.255.255/7"}, {"1.2.3.4/8", "1.255.255.255/8"}, {"1.2.3.4/9", "1.127.255.255/9"}, {"1.2.3.4/31", "1.2.3.5/31"}, {"1.2.3.4/32", "1.2.3.4"},
		{"192.168.0.129/25", "192.168.0.255/25"}, {"::/0", "ffff:ffff:ffff:ffff:ffff:ffff:ffff:ffff/0"}, {"2001:db8::1/63", "2001:db8:0:1:ffff:ffff:ffff:ffff/63"}, {"2001:db8::1/64", "2001:db8::ffff:ffff:ffff:ffff/64"}, {"2001:db8::1/65", "2001:db8::7fff:ffff:ffff:ffff/65"}, {"2001:db8::1/113", "2001:db8::7fff/113"}, {"2001:db8::1/127", "2001:db8::1/127"}, {"2001:db8::2/127", "2001:db8::3/127"}, {"::1/128", "::1"}, {"::ffff:1.2.3.4/120", "::ffff:1.2.3.255/120"},
	} {
		t.Run(tc.in, func(t *testing.T) {
			inetFormatScenario(t, func(p *fixture.Pair, b fixture.Backend) {
				inetFormatBoth(t, p, b, fixture.Select("SELECT broadcast($1::inet) AS b,text($1::inet) AS original", []string{"b", "original"}, []string{"INET", "TEXT"}, [][]any{{tc.out, tc.in}}, tc.in))
			})
		})
	}
	inetFormatScenario(t, func(p *fixture.Pair, b fixture.Backend) {
		for _, tc := range []struct{ in, out string }{{"10.1/16", "10.1.255.255/16"}, {"1.2.3.4/32", "1.2.3.4"}, {"::1/128", "::1"}} {
			inetFormatBoth(t, p, b, fixture.Select("SELECT broadcast($1::cidr) AS b", []string{"b"}, []string{"INET"}, [][]any{{tc.out}}, tc.in))
		}
	})
}

func TestReferenceINETFormatFeatureSmoke(t *testing.T) {
	inetFormatScenario(t, func(p *fixture.Pair, b fixture.Backend) {
		inetFormatBoth(t, p, b, fixture.Select("SELECT abbrev(INET '192.168.1.5/24') AS a,abbrev(CIDR '10.1') AS c,host(INET '192.168.1.5/24') AS h,text(INET '::1') AS t,family(INET '::1') AS f,masklen(INET '::1') AS m,broadcast(INET '2001:db8::1/113') AS b,inet_same_family(INET '1.2.3.4',INET '::ffff:1.2.3.4') AS s", []string{"a", "c", "h", "t", "f", "m", "b", "s"}, []string{"TEXT", "TEXT", "TEXT", "TEXT", "INT4", "INT4", "INET", "BOOL"}, [][]any{{"192.168.1.5/24", "10.1/16", "192.168.1.5", "::1/128", int64(6), int64(128), "2001:db8::7fff/113", false}}))
	})
}

func TestReferenceINETFormatRoutineIdentity(t *testing.T) {
	inetFormatScenario(t, func(p *fixture.Pair, b fixture.Backend) {
		for _, tc := range []struct {
			name, sig          string
			oid, result, arity int64
		}{
			{"abbrev", "inet", 598, 25, 1}, {"abbrev", "cidr", 599, 25, 1}, {"host", "inet", 699, 25, 1}, {"text", "inet", 730, 25, 1}, {"family", "inet", 711, 23, 1}, {"masklen", "inet", 697, 23, 1}, {"inet_same_family", "inet,inet", 4071, 16, 2}, {"broadcast", "inet", 698, 869, 1},
		} {
			t.Run(fmt.Sprint(tc.oid), func(t *testing.T) {
				inetFormatBoth(t, p, b, fixture.Select("SELECT oid::regprocedure::text AS identity,proname,pronamespace::bigint AS namespace,pronargs,prorettype::bigint AS result,proisstrict,provolatile,proparallel FROM pg_proc WHERE oid=$1", []string{"identity", "proname", "namespace", "pronargs", "result", "proisstrict", "provolatile", "proparallel"}, []string{"TEXT", "NAME", "INT8", "INT2", "INT8", "BOOL", "CHAR", "CHAR"}, [][]any{{tc.name + "(" + tc.sig + ")", tc.name, int64(11), tc.arity, tc.result, true, "i", "s"}}, tc.oid))
			})
		}
	})
}
