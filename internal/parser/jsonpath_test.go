package parser

import "testing"

func TestJSONPathTypedLiteralAST(t *testing.T) {
	for _, query := range []string{`SELECT jsonpath 'strict $.a' AS v`, `SELECT pg_catalog.jsonpath '$' AS v`, `SELECT "jsonpath" '$' AS v`} {
		statements, err := Parse(query)
		if err != nil || len(statements) != 1 {
			t.Fatal(query, err)
		}
		selected := statements[0].(*Select)
		if selected.From != nil || len(selected.Targets) != 1 {
			t.Fatal("typed literal target lost")
		}
		literal, ok := selected.Targets[0].(TypedLiteralExpr)
		if !ok || literal.TypeName.Name != "jsonpath" || literal.Pos.Character <= 0 {
			t.Fatal("typed literal AST", selected.Targets[0])
		}
		if query == `SELECT pg_catalog.jsonpath '$' AS v` && (literal.TypeName.Schema == nil || literal.TypeName.Schema.Text != "pg_catalog") {
			t.Fatal("qualified identity lost")
		}
	}
	if lookupKeyword("jsonpath") != nil {
		t.Fatal("JSONPath became reserved")
	}
}
