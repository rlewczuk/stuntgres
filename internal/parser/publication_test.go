package parser

import (
	"strings"
	"testing"
	"unicode/utf8"
)

func TestPublicationGrammar(t *testing.T) {
	for _, tc := range []struct{ query, action string }{
		{`CREATE PUBLICATION "P雪" FOR TABLE ONLY s.t(b,a) WHERE (a>0), s.u *, TABLES IN SCHEMA s,CURRENT_SCHEMA WITH(publish='insert',publish_via_partition_root)`, "create"},
		{`CREATE PUBLICATION p`, "create"}, {`CREATE PUBLICATION p FOR ALL TABLES WITH(publish_generated_columns=stored)`, "create"},
		{`ALTER PUBLICATION p ADD TABLE t WHERE (CASE WHEN a IS NULL THEN false ELSE a>0 END), TABLES IN SCHEMA s`, "add"},
		{`ALTER PUBLICATION p SET TABLE ONLY (t)(b,a)`, "set"}, {`ALTER PUBLICATION p DROP TABLE t`, "remove"},
		{`ALTER PUBLICATION p DROP TABLES IN SCHEMA CURRENT_SCHEMA`, "remove"},
		{`ALTER PUBLICATION p SET(publish='insert',publish='delete')`, "options"},
		{`ALTER PUBLICATION p OWNER TO CURRENT_USER`, "owner"}, {`ALTER PUBLICATION p RENAME TO "P 2"`, "rename"},
		{`DROP PUBLICATION IF EXISTS p,q CASCADE`, "drop"},
	} {
		nodes, err := Parse(tc.query)
		if err != nil || len(nodes) != 1 {
			t.Fatal(tc.query, nodes, err)
		}
		s, ok := nodes[0].(*Publication)
		if !ok || s.Action != tc.action {
			t.Fatal(tc.query, nodes)
		}
		if strings.Contains(tc.query, `"P雪"`) {
			if s.Name.Text != "P雪" || len(s.Members) != 4 || !s.Members[0].Only || len(s.Members[0].Columns) != 2 || s.Members[0].Columns[0].Text != "b" || s.Members[0].Filter == nil || !s.Members[1].Star || !s.Members[3].CurrentSchema || len(s.Options) != 2 || s.Options[1].Supplied {
				t.Fatal("lost publication syntax", s)
			}
			offset := strings.Index(tc.query, "publish_via_partition_root")
			pos := s.Options[1].Name.Pos
			if pos.Offset != offset || pos.Character != utf8.RuneCountInString(tc.query[:offset])+1 {
				t.Fatal("position provenance", pos)
			}
		}
		if s.Action == "owner" && (!s.OwnerIdentity || s.Target.Text != "current_user") {
			t.Fatal(s)
		}
		if s.Action == "drop" && (!s.IfExists || len(s.Names) != 2 || s.Mode != DropCascade) {
			t.Fatal(s)
		}
		if s.Action == "options" && (len(s.Options) != 2 || s.Options[0].Name.Text != s.Options[1].Name.Text) {
			t.Fatal("duplicate option erased", s)
		}
	}
	for _, query := range []string{`CREATE PUBLICATION p FOR TABLE`, `CREATE PUBLICATION p FOR ALL SEQUENCES`, `CREATE PUBLICATION p FOR TABLE t EXCEPT t`, `CREATE PUBLICATION p WITH()`, `CREATE PUBLICATION p FOR TABLE t WHERE true`, `ALTER PUBLICATION p SET()`, `DROP PUBLICATION p CASCADE junk`, `CREATE PUBLICATION p trailing`} {
		if nodes, err := Parse("SELECT 1;" + query); err == nil || nodes != nil {
			t.Fatal("accepted incomplete or trailing script", query, nodes, err)
		}
	}
}
