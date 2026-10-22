package parser

import "testing"

func TestPathParserAdmission(t *testing.T) {
	for _, head := range []string{"path", "\"path\"", "pg_catalog.path", "pg_catalog.\"path\""} {
		statements, err := Parse("SELECT " + head + " '[(1,2),(3,4)]'")
		if err != nil || len(statements) != 1 {
			t.Fatal(head, statements, err)
		}
		literal, ok := statements[0].(*Select).Targets[0].(TypedLiteralExpr)
		if !ok || literal.TypeName.Name != "path" || literal.Text != "[(1,2),(3,4)]" || literal.RequiresDomain || literal.Pos.Character != 9+len(head) {
			t.Fatal(head, literal)
		}
		if len(head) > 10 && (literal.TypeName.Schema == nil || literal.TypeName.Schema.Text != "pg_catalog") {
			t.Fatal(head, literal.TypeName)
		}
	}
	// The keyword spelling does not grant builtin authority to other schemas.
	statements, err := Parse("SELECT custom.path '[(1,2)]'")
	if err != nil || len(statements) != 1 {
		t.Fatal(statements, err)
	}
	literal := statements[0].(*Select).Targets[0].(TypedLiteralExpr)
	if !literal.RequiresDomain || literal.TypeName.Schema == nil || literal.TypeName.Schema.Text != "custom" {
		t.Fatal(literal)
	}
}
