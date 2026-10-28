package parser

import (
	"strings"
	"testing"
	"unicode/utf8"
)

func TestSubscriptionGrammar(t *testing.T) {
	cases := []struct{ query, action string }{
		{`CREATE SUBSCRIPTION "S雪" CONNECTION 'host=x' PUBLICATION "Pub A",pub_b WITH (connect=false,binary,streaming=parallel,slot_name=NONE)`, "create"},
		{`ALTER SUBSCRIPTION s CONNECTION ''`, "connection"}, {`ALTER SUBSCRIPTION s SET PUBLICATION p,q WITH (refresh=false)`, "set"},
		{`ALTER SUBSCRIPTION s ADD PUBLICATION p WITH (refresh=false)`, "add"}, {`ALTER SUBSCRIPTION s DROP PUBLICATION p WITH (refresh=false)`, "remove"},
		{`ALTER SUBSCRIPTION s REFRESH PUBLICATION WITH (copy_data=false)`, "refresh"}, {`ALTER SUBSCRIPTION s ENABLE`, "enable"}, {`ALTER SUBSCRIPTION s DISABLE`, "disable"},
		{`ALTER SUBSCRIPTION s SET (binary=1,origin='NONE')`, "options"}, {`ALTER SUBSCRIPTION s SKIP (lsn='1/1234')`, "skip"},
		{`ALTER SUBSCRIPTION s OWNER TO CURRENT_ROLE`, "owner"}, {`ALTER SUBSCRIPTION s RENAME TO "new name"`, "rename"}, {`DROP SUBSCRIPTION IF EXISTS s CASCADE`, "drop"},
	}
	for _, tc := range cases {
		nodes, err := Parse(tc.query)
		if err != nil || len(nodes) != 1 {
			t.Fatal(tc.query, nodes, err)
		}
		s, ok := nodes[0].(*Subscription)
		if !ok || s.Action != tc.action {
			t.Fatal(tc.query, nodes)
		}
		if s.Action == "create" {
			if s.Name.Text != "S雪" || s.Connection != "host=x" || len(s.Publications) != 2 || s.Publications[0].Text != "Pub A" || s.Publications[1].Text != "pub_b" || len(s.Options) != 4 || s.Options[1].Supplied || s.Options[0].Value != "false" {
				t.Fatal("lost subscription syntax", s)
			}
			at := strings.Index(tc.query, "streaming")
			if s.Options[2].Name.Pos.Offset != at || s.Options[2].Name.Pos.Character != utf8.RuneCountInString(tc.query[:at])+1 {
				t.Fatal("option positions", s.Options)
			}
		}
		if s.Action == "owner" && (!s.OwnerIdentity || s.Target.Text != "current_role") {
			t.Fatal(s)
		}
		if s.Action == "drop" && (!s.IfExists || s.Mode != DropCascade) {
			t.Fatal(s)
		}
	}
	for _, query := range []string{
		`CREATE SUBSCRIPTION IF NOT EXISTS s CONNECTION '' PUBLICATION p`,
		`CREATE SUBSCRIPTION s CONNECTION $1 PUBLICATION p`,
		`CREATE SUBSCRIPTION s CONNECTION '' PUBLICATION`,
		`CREATE SUBSCRIPTION s CONNECTION '' PUBLICATION p WITH ()`,
		`ALTER SUBSCRIPTION s "enable"`, `ALTER SUBSCRIPTION s SKIP ()`,
		`ALTER SUBSCRIPTION s SET (binary=$1)`, `DROP SUBSCRIPTION s,t`,
		`CREATE SUBSCRIPTION s CONNECTION '' PUBLICATION p junk`,
		`ALTER SUBSCRIPTION s ENABLE junk`, `DROP SUBSCRIPTION s CASCADE junk`,
	} {
		if nodes, err := Parse("SELECT 1;" + query); err == nil || nodes != nil {
			t.Fatal("accepted malformed complete script", query, nodes, err)
		}
	}
}
