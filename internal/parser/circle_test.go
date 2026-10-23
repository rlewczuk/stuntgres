package parser

import "testing"

func TestGeometryConversionCLiteralSyntax(t *testing.T) {
	for _, head := range []string{"circle", "\"circle\"", "pg_catalog.circle", "pg_catalog.\"circle\"", "custom.circle"} {
		statements, err := Parse("SELECT " + head + " '<(0,0),1>'")
		if err != nil || len(statements) != 1 {
			t.Fatal(head, statements, err)
		}
		literal, ok := statements[0].(*Select).Targets[0].(TypedLiteralExpr)
		if !ok || literal.TypeName.Name != "circle" || literal.Text != "<(0,0),1>" || literal.Pos.Character != 9+len(head) {
			t.Fatal("literal AST/source position", head, literal)
		}
		custom := head == "custom.circle"
		if literal.RequiresDomain != custom {
			t.Fatal("literal schema authority", head, literal)
		}
		if custom && (literal.TypeName.Schema == nil || literal.TypeName.Schema.Text != "custom") {
			t.Fatal("custom schema lost", literal)
		}
		if len(head) > 10 && !custom && (literal.TypeName.Schema == nil || literal.TypeName.Schema.Text != "pg_catalog") {
			t.Fatal("builtin schema lost", literal)
		}
	}
}
