package parser

import "testing"

func TestMACADDR8ParserAdmission(t *testing.T) {
	for _, prefix := range []string{"", "pg_catalog.", "custom."} {
		statements, err := Parse("SELECT " + prefix + "macaddr8 '08002b010203'")
		if err != nil || len(statements) != 1 {
			t.Fatal(statements, err)
		}
		literal, ok := statements[0].(*Select).Targets[0].(TypedLiteralExpr)
		if !ok || literal.TypeName.Name != "macaddr8" || literal.Text != "08002b010203" || literal.Pos.Character != 17+len(prefix) {
			t.Fatal(literal)
		}
		if literal.RequiresDomain != (prefix == "custom.") {
			t.Fatal("schema admission", literal)
		}
		if prefix != "" && literal.TypeName.Schema == nil {
			t.Fatal(literal.TypeName)
		}
	}
	statements, err := Parse("CREATE TABLE mac8_syntax(v macaddr8,a macaddr8[],m macaddr8(3))")
	if err != nil || len(statements) != 1 {
		t.Fatal(statements, err)
	}
	columns := statements[0].(*CreateTable).Elements
	if columns[0].Column.TypeName.Name != "macaddr8" || len(columns[1].Column.TypeName.ArrayBounds) != 1 || len(columns[2].Column.TypeName.Modifiers) != 1 {
		t.Fatal(columns)
	}
}
