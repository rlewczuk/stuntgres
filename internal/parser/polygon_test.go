package parser

import "testing"

func TestPolygonParserAdmission(t *testing.T) {
	for _, head := range []string{"polygon", "\"polygon\"", "pg_catalog.polygon", "pg_catalog.\"polygon\""} {
		statements, err := Parse("SELECT " + head + " '((0,0),(4,0),(0,4))'")
		if err != nil || len(statements) != 1 {
			t.Fatal(head, statements, err)
		}
		literal, ok := statements[0].(*Select).Targets[0].(TypedLiteralExpr)
		if !ok || literal.TypeName.Name != "polygon" || literal.Text != "((0,0),(4,0),(0,4))" || literal.RequiresDomain || literal.Pos.Character != 9+len(head) {
			t.Fatal(head, literal)
		}
		if len(head) > 10 && (literal.TypeName.Schema == nil || literal.TypeName.Schema.Text != "pg_catalog") {
			t.Fatal(head, literal.TypeName)
		}
	}
	// Matching the builtin spelling must not confer builtin authority on an
	// unrelated schema's type; retain the binder's domain-only admission gate.
	statements, err := Parse("SELECT custom.polygon '((0,0),(4,0),(0,4))'")
	if err != nil || len(statements) != 1 {
		t.Fatal(statements, err)
	}
	literal := statements[0].(*Select).Targets[0].(TypedLiteralExpr)
	if !literal.RequiresDomain || literal.TypeName.Schema == nil || literal.TypeName.Schema.Text != "custom" {
		t.Fatal(literal)
	}
}
