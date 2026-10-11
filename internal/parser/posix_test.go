package parser

import "testing"

func TestPosixOperatorComposition(t *testing.T) {
	for _, op := range []string{"~", "~*", "!~", "!~*"} {
		for _, query := range []string{
			"SELECT v " + op + " p FROM t",
			"SELECT substring(v FROM p) " + op + " p FROM t",
			"SELECT v " + op + " pg_catalog.substring(p,p) FROM t",
			"SELECT substring(v FROM p) OPERATOR(pg_catalog." + op + ") pg_catalog.substring(p,p) FROM t",
		} {
			node, ok := orderParse(t, query).Targets[0].(OperatorExpr)
			if !ok || node.SQLSimilar || len(node.Args) != 2 || node.Name.Parts[len(node.Name.Parts)-1].Text != op {
				t.Fatal(query, node)
			}
		}
	}
}

func TestPosixSubstringSyntax(t *testing.T) {
	for _, c := range []struct {
		query  string
		syntax FunctionSyntax
	}{
		{"SELECT substring(v,p) FROM t", OrdinaryFunctionSyntax},
		{"SELECT pg_catalog.substring(v,p) FROM t", OrdinaryFunctionSyntax},
		{"SELECT substring(v FROM p) FROM t", SQLFunctionSyntax},
	} {
		node, ok := orderParse(t, c.query).Targets[0].(FunctionExpr)
		if !ok || len(node.Args) != 2 || node.Syntax != c.syntax || node.Name.Parts[len(node.Name.Parts)-1].Text != "substring" {
			t.Fatal(c.query, node)
		}
	}
}
