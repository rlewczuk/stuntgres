package parser

import (
	"strings"
	"testing"
	"unicode/utf8"
)

func TestINETSubnetParser(t *testing.T) {
	for _, op := range []string{"<<", "<<=", ">>", ">>=", "&&"} {
		for _, symbol := range []string{op, "OPERATOR(pg_catalog." + op + ")"} {
			for _, prefix := range []string{"", "/*é*/ "} {
				q := prefix + "SELECT INET '::1'" + symbol + "/*operand*/INET '::/0'"
				statements, err := Parse(q)
				if err != nil || len(statements) != 1 {
					t.Fatal(q, err)
				}
				node, ok := statements[0].(*Select).Targets[0].(OperatorExpr)
				if !ok || len(node.Args) != 2 || node.Name.Parts[len(node.Name.Parts)-1].Text != op {
					t.Fatal(node)
				}
				expected := utf8.RuneCountInString(q[:strings.Index(q, symbol)]) + 1
				if node.Pos.Character != expected {
					t.Fatal(q, node.Pos, expected)
				}
				if symbol != op && (len(node.Name.Parts) != 2 || node.Name.Parts[0].Text != "pg_catalog") {
					t.Fatal(node.Name)
				}
			}
		}
	}
}
