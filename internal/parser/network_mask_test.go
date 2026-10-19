package parser

import (
	"strings"
	"testing"
)

func TestNetworkMaskParserAdmission(t *testing.T) {
	for _, name := range []string{"hostmask", "netmask", "network", "inet_merge", "set_masklen"} {
		args := "ip"
		if name == "inet_merge" {
			args = "ip,ip"
		}
		if name == "set_masklen" {
			args = "ip,16"
		}
		for _, spelling := range []string{name, "pg_catalog." + name, `"` + name + `"`, `pg_catalog."` + name + `"`, `"` + strings.ToUpper(name) + `"`} {
			q := "SELECT " + spelling + "(" + args + ") FROM r"
			statements, err := Parse(q)
			if err != nil || len(statements) != 1 {
				t.Fatal(q, err)
			}
			call, ok := statements[0].(*Select).Targets[0].(FunctionExpr)
			want := name
			if strings.Contains(spelling, strings.ToUpper(name)) {
				want = strings.ToUpper(name)
			}
			if !ok || call.Name.Parts[len(call.Name.Parts)-1].Text != want {
				t.Fatal("lost call identity", q)
			}
		}
		for _, q := range []string{"SELECT " + name + " FROM r", "CREATE TABLE r(" + name + " integer)", "SELECT " + name + "(NULL)", "SELECT " + name + "(DISTINCT " + args + ")", "SELECT " + name + "(" + args + " ORDER BY 1)", "SELECT " + name + "(" + args + ") FILTER (WHERE true)", "SELECT " + name + "(" + args + ") OVER ()", "SELECT " + name + "(x => ip)"} {
			if _, err := Parse(q); err != nil {
				t.Fatal(q, err)
			}
		}
		if _, err := Parse("SELECT " + name + "(" + args + ") trailing garbage"); err == nil {
			t.Fatal("unconsumed suffix", name)
		}
	}
	if _, err := Parse("CREATE TABLE r(ip inet,n cidr GENERATED ALWAYS AS (network(ip)) STORED,CHECK (masklen(network(ip))<=24))"); err != nil {
		t.Fatal(err)
	}
}
