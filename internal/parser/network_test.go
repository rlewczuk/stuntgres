package parser

import "testing"

func TestNetworkTypedLiteralsAndDeclarations(t *testing.T) {
	for _, name := range []string{"inet", "cidr"} {
		for _, prefix := range []string{"", "pg_catalog."} {
			statements, err := Parse("SELECT " + prefix + name + " '::1'")
			if err != nil || len(statements) != 1 {
				t.Fatal(statements, err)
			}
			literal, ok := statements[0].(*Select).Targets[0].(TypedLiteralExpr)
			if !ok || literal.TypeName.Name != name || literal.Text != "::1" || literal.Pos.Character != 8+len(prefix)+len(name)+1 {
				t.Fatal(literal)
			}
			if prefix != "" && (literal.TypeName.Schema == nil || literal.TypeName.Schema.Text != "pg_catalog") {
				t.Fatal(literal.TypeName)
			}
		}
		statements, err := Parse("CREATE TABLE n(h " + name + ",a " + name + "[],m " + name + "(3))")
		if err != nil || len(statements) != 1 {
			t.Fatal(statements, err)
		}
		columns := statements[0].(*CreateTable).Elements
		if columns[0].Column.TypeName.Name != name || len(columns[1].Column.TypeName.ArrayBounds) != 1 || len(columns[2].Column.TypeName.Modifiers) != 1 {
			t.Fatal(columns)
		}
		// Parser preserves syntax; concrete/modifier/array admission belongs to the binder.
	}
}
