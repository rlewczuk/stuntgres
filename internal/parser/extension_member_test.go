package parser

import "testing"

func TestExtensionInvalidMemberTargets(t *testing.T) {
	for _, action := range []string{"ADD", "DROP"} {
		for _, kind := range []string{"DATABASE", "EXTENSION", "INDEX", "PUBLICATION", "ROLE", "STATISTICS", "SUBSCRIPTION", "TABLESPACE"} {
			q := "ALTER EXTENSION missing_ext " + action + " " + kind + " eo_invalid"
			statements, err := Parse(q)
			if err != nil || len(statements) != 1 {
				t.Fatal(q, statements, err)
			}
			stmt, ok := statements[0].(*AlterExtension)
			if !ok || stmt.Member.Kind != "invalid" || stmt.Name.Text != "missing_ext" {
				t.Fatalf("invalid member syntax lost typed utility target: %s: %#v", q, statements[0])
			}
			for _, malformed := range []string{
				"ALTER EXTENSION missing_ext " + action + " " + kind,
				q + " trailing",
			} {
				if statements, err := Parse(malformed); err == nil || len(statements) != 0 {
					t.Fatal("accepted incomplete or trailing member syntax", malformed, statements, err)
				}
			}
		}
	}
}

func TestExtensionSearchMemberTargets(t *testing.T) {
	for _, action := range []string{"ADD", "DROP"} {
		for _, kind := range []string{"dictionary", "template", "configuration", "parser"} {
			query := "ALTER EXTENSION target " + action + " TEXT SEARCH " + kind + " source.member"
			statements, err := Parse(query)
			if err != nil || len(statements) != 1 {
				t.Fatal(query, statements, err)
			}
			stmt, ok := statements[0].(*AlterExtension)
			if !ok || stmt.Action != map[string]string{"ADD": "add", "DROP": "drop"}[action] || stmt.Member.Kind != "text search "+kind || len(stmt.Member.Name.Parts) != 2 || stmt.Member.Name.Parts[0].Text != "source" || stmt.Member.Name.Parts[1].Text != "member" {
				t.Fatalf("text search member identity lost: %s: %#v", query, statements[0])
			}
			for _, malformed := range []string{query + " trailing", "ALTER EXTENSION target " + action + " TEXT SEARCH " + kind} {
				if statements, err := Parse(malformed); err == nil || len(statements) != 0 {
					t.Fatal("accepted incomplete or trailing member syntax", malformed, statements, err)
				}
			}
		}
	}
}
