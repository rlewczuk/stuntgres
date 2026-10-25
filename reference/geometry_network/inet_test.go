package reference_test

import (
	"fmt"
	"stuntgres/reference/fixture"
	"testing"
)

func TestReferenceINETCodecStorage(t *testing.T) {
	cases := []struct{ typ, input, output string }{
		{"inet", "192.168.0.1/24", "192.168.0.1/24"}, {"inet", "192.168.1.5/32", "192.168.1.5"},
		{"inet", "192.168/16", "192.168.0.0/16"}, {"inet", "192.168/23", "192.168.0.0/23"},
		{"inet", "255.255.000.000", "255.255.0.0"}, {"inet", "255.255.255.001", "255.255.255.1"},
		{"inet", "1.2.3.4/0", "1.2.3.4/0"}, {"inet", "::1/128", "::1"},
		{"inet", "2001:DB8:0000:0000:0000:0000:0000:0001/113", "2001:db8::1/113"},
		{"inet", "1:0:0:2:0:0:3:4", "1::2:0:0:3:4"},
		{"inet", "::4.3.2.1", "::4.3.2.1"}, {"inet", "::ffff:1.2.3.4", "::ffff:1.2.3.4"},
		{"cidr", "192.168.100.128/25", "192.168.100.128/25"}, {"cidr", "192.168/24", "192.168.0.0/24"},
		{"cidr", "192.168/25", "192.168.0.0/25"}, {"cidr", "192.168.1", "192.168.1.0/24"},
		{"cidr", "192.168", "192.168.0.0/24"}, {"cidr", "128.1", "128.1.0.0/16"},
		{"cidr", "128", "128.0.0.0/16"}, {"cidr", "128.1.2", "128.1.2.0/24"},
		{"cidr", "10.1.2", "10.1.2.0/24"}, {"cidr", "10.1", "10.1.0.0/16"},
		{"cidr", "10", "10.0.0.0/8"}, {"cidr", "10.0.0.0", "10.0.0.0/32"},
		{"cidr", "10.1.2.3/32", "10.1.2.3/32"}, {"cidr", "0/0", "0.0.0.0/0"},
		{"cidr", "224", "224.0.0.0/4"}, {"cidr", "225", "225.0.0.0/8"}, {"cidr", "240", "240.0.0.0/32"},
		{"cidr", "0xC0A8", "192.168.0.0/24"}, {"cidr", "0xA", "160.0.0.0/16"},
		{"cidr", "::1", "::1/128"}, {"cidr", "2001:db8::/113", "2001:db8::/113"},
		{"cidr", "::/0", "::/0"}, {"cidr", "::ffff:1.2.3.4/128", "::ffff:1.2.3.4/128"},
		{"cidr", "2001:4f8:3:ba::/64", "2001:4f8:3:ba::/64"},
		{"cidr", "2001:4f8:3:ba:2e0:81ff:fe22:d1f1/128", "2001:4f8:3:ba:2e0:81ff:fe22:d1f1/128"},
		{"cidr", "::ffff:1.2.3.0/120", "::ffff:1.2.3.0/120"}, {"cidr", "::ffff:1.2.3.0/128", "::ffff:1.2.3.0/128"},
	}
	for i, c := range cases {
		t.Run(fmt.Sprintf("%02d_%s", i, c.typ), func(t *testing.T) {
			p := fixture.NewScenario(t)
			p.Step(t, fixture.Exec("CREATE TABLE n(v "+c.typ+")", 0))
			p.Step(t, fixture.Exec("INSERT INTO n VALUES($1)", 1, c.input))
			typ := "INET"
			if c.typ == "cidr" {
				typ = "CIDR"
			}
			p.Step(t, fixture.Select("SELECT v FROM n", []string{"v"}, []string{typ}, [][]any{{c.output}}))
			p.Step(t, fixture.Select("SELECT pg_catalog."+c.typ+" '"+c.input+"' AS v", []string{"v"}, []string{typ}, [][]any{{c.output}}))
		})
	}
}

func TestReferenceINETOrderingAndCasts(t *testing.T) {
	p := fixture.NewScenario(t)
	p.Step(t, fixture.Exec("CREATE TABLE n(v INET)", 0))
	values := []any{"10.255.255.255/8", "10.0.0.0/16", "10.0.0.1/16", "255.255.255.255", "::1", "::4.3.2.1", "::10.2.3.4", "::ffff:1.2.3.4", nil}
	p.Step(t, fixture.Exec("INSERT INTO n VALUES($1),($2),($3),($4),($5),($6),($7),($8),($9)", 9, values...))
	asc := make([][]any, len(values))
	desc := make([][]any, len(values))
	for i, v := range values {
		asc[i] = []any{v}
		desc[len(values)-1-i] = []any{v}
	}
	p.Step(t, fixture.Select("SELECT v FROM n ORDER BY v", []string{"v"}, []string{"INET"}, asc))
	p.Step(t, fixture.Prepared(fixture.Select("SELECT v FROM n ORDER BY v DESC", []string{"v"}, []string{"INET"}, desc)))
	for _, q := range []string{
		"SELECT '10.255.255.255/8'::inet < '10.0.0.0/16'::inet AS v",
		"SELECT '10.0.0.1/8'::inet < '10.0.0.2/8'::inet AS v",
		"SELECT '10.0.0.1/8'::inet < '10.0.0.1/16'::inet AS v",
		"SELECT '255.255.255.255'::inet < '::1'::inet AS v",
		"SELECT '10/8'::cidr = '10.0.0.0/8'::inet AS v",
		"SELECT '1.2.3.4/0'::inet < '1.2.3.5/0'::inet AS v",
	} {
		p.Step(t, fixture.Select(q, []string{"v"}, []string{"BOOL"}, [][]any{{true}}))
	}
	for _, op := range []string{"=", "<>", "<", "<=", ">", ">="} {
		want := op == "<>" || op == "<" || op == "<="
		p.Step(t, fixture.Select("SELECT '10.0.0.1/8'::inet "+op+" '10.0.0.2/8'::inet AS v", []string{"v"}, []string{"BOOL"}, [][]any{{want}}))
		p.Step(t, fixture.Select("SELECT NULL::inet "+op+" '10/8'::cidr AS v", []string{"v"}, []string{"BOOL"}, [][]any{{nil}}))
	}
	for _, c := range []struct{ input, output string }{
		{"192.168.0.129/25", "192.168.0.128/25"}, {"2001:db8::ffff/113", "2001:db8::8000/113"},
		{"1.2.3.4/0", "0.0.0.0/0"}, {"2001:db8::1/0", "::/0"},
	} {
		for _, q := range []string{"SELECT ('" + c.input + "'::inet)::cidr AS v", "SELECT CAST(CAST('" + c.input + "' AS inet) AS cidr) AS v"} {
			p.Step(t, fixture.Select(q, []string{"v"}, []string{"CIDR"}, [][]any{{c.output}}))
		}
	}
	p.Step(t, fixture.Exec("CREATE TABLE source(h INET,c CIDR)", 0))
	p.Step(t, fixture.Exec("INSERT INTO source VALUES('192.168.0.129/25','10')", 1))
	p.Step(t, fixture.Exec("CREATE TABLE target(h INET,c CIDR)", 0))
	p.Step(t, fixture.Exec("INSERT INTO target SELECT c,h FROM source", 1))
	p.Step(t, fixture.Select("SELECT h,c FROM target", []string{"h", "c"}, []string{"INET", "CIDR"}, [][]any{{"10.0.0.0/8", "192.168.0.128/25"}}))
	p.Step(t, fixture.Select("SELECT h,c FROM source", []string{"h", "c"}, []string{"INET", "CIDR"}, [][]any{{"192.168.0.129/25", "10.0.0.0/8"}}))
	p.Step(t, fixture.Select("SELECT ('192.168.1.5'::inet)::text AS h,('10'::cidr)::text AS c", []string{"h", "c"}, []string{"TEXT", "TEXT"}, [][]any{{"192.168.1.5/32", "10.0.0.0/8"}}))
	p.Step(t, fixture.Select("SELECT CAST('192.168.1.5'::text AS inet) AS h,CAST('10'::text AS cidr) AS c", []string{"h", "c"}, []string{"INET", "CIDR"}, [][]any{{"192.168.1.5", "10.0.0.0/8"}}))
	p.Step(t, fixture.Select("SELECT NULL::cidr::inet AS h,NULL::inet::cidr AS c", []string{"h", "c"}, []string{"INET", "CIDR"}, [][]any{{nil, nil}}))
}
