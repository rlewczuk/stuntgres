package parser

import "testing"

func TestMACADDRParserAdmission(t *testing.T) {
	for _, prefix := range []string{"", "pg_catalog."} {
		statements, err := Parse("SELECT " + prefix + "macaddr '08002b010203'")
		if err != nil || len(statements) != 1 {
			t.Fatal(statements, err)
		}
		literal, ok := statements[0].(*Select).Targets[0].(TypedLiteralExpr)
		if !ok || literal.TypeName.Name != "macaddr" || literal.Text != "08002b010203" || literal.RequiresDomain || literal.Pos.Character != 16+len(prefix) {
			t.Fatal(literal)
		}
		if prefix != "" && (literal.TypeName.Schema == nil || literal.TypeName.Schema.Text != "pg_catalog") {
			t.Fatal(literal.TypeName)
		}
	}
	statements, err := Parse("CREATE TABLE mac_syntax(v macaddr,a macaddr[],m macaddr(3))")
	if err != nil || len(statements) != 1 {
		t.Fatal(statements, err)
	}
	columns := statements[0].(*CreateTable).Elements
	if columns[0].Column.TypeName.Name != "macaddr" || len(columns[1].Column.TypeName.ArrayBounds) != 1 || len(columns[2].Column.TypeName.Modifiers) != 1 {
		t.Fatal(columns)
	}
	// Parsing preserves declaration syntax; codec/modifier/array admission
	// belongs to analysis, not a parser-level unsupported-type shortcut.
}
