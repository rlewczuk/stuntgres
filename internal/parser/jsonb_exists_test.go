package parser

import "testing"

func TestJSONBExistsOperatorComposition(t *testing.T) {
	for _, op := range []string{"?", "?|", "?&"} {
		for _, query := range []string{
			"SELECT doc " + op + " keys FROM t",
			"SELECT jsonb_object(keys) " + op + " keys FROM t",
			"SELECT doc " + op + " lower(key) FROM t",
			"SELECT doc OPERATOR(pg_catalog." + op + ") keys FROM t",
			"SELECT doc " + op + "/* comment */ keys FROM t",
		} {
			node, ok := orderParse(t, query).Targets[0].(OperatorExpr)
			if !ok || len(node.Args) != 2 || node.Name.Parts[len(node.Name.Parts)-1].Text != op || node.Pos.Character == 0 {
				t.Fatal(query, node)
			}
		}
	}
	orderParse(t, "SELECT doc ? 'a' AND doc ? 'b' FROM t")
}
