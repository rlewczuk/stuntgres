package parser

import "testing"

func TestLineParserAdmission(t *testing.T) {
	for _, prefix := range []string{"", "pg_catalog."} {
		statements, err := Parse("SELECT " + prefix + "line '{1,2,3}'")
		if err != nil || len(statements) != 1 {
			t.Fatal(statements, err)
		}
		literal, ok := statements[0].(*Select).Targets[0].(TypedLiteralExpr)
		if !ok || literal.TypeName.Name != "line" || literal.Text != "{1,2,3}" || literal.RequiresDomain || literal.Pos.Character != 13+len(prefix) {
			t.Fatal(literal)
		}
	}
}
