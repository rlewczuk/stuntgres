package parser

import "testing"

func TestLsegParserAdmission(t *testing.T) {
	for _, head := range []string{"lseg", "\"lseg\"", "pg_catalog.lseg", "pg_catalog.\"lseg\""} {
		statements, err := Parse("SELECT " + head + " '[(1,4),(3,2)]'")
		if err != nil || len(statements) != 1 {
			t.Fatal(head, statements, err)
		}
		literal, ok := statements[0].(*Select).Targets[0].(TypedLiteralExpr)
		if !ok || literal.TypeName.Name != "lseg" || literal.Text != "[(1,4),(3,2)]" || literal.RequiresDomain || literal.Pos.Character != 9+len(head) {
			t.Fatal(head, literal)
		}
		if len(head) > 10 && (literal.TypeName.Schema == nil || literal.TypeName.Schema.Text != "pg_catalog") {
			t.Fatal(head, literal.TypeName)
		}
	}
	// Nonbuiltin schemas retain domain-only syntax authority; recognition of
	// the spelling lseg must not bypass the binder's real type resolution.
	statements, err := Parse("SELECT custom.lseg '[(1,4),(3,2)]'")
	if err != nil || len(statements) != 1 {
		t.Fatal(statements, err)
	}
	literal := statements[0].(*Select).Targets[0].(TypedLiteralExpr)
	if !literal.RequiresDomain || literal.TypeName.Schema == nil || literal.TypeName.Schema.Text != "custom" {
		t.Fatal(literal)
	}
}
