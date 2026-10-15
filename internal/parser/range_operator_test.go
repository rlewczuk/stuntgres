package parser

import (
	"strings"
	"testing"
	"unicode/utf8"
)

func TestRangeOperatorGrammar(t *testing.T) {
	for _, op := range []string{"@>", "<@", "&&", "<<", ">>", "&<", "&>", "-|-", "+", "*", "-", "=", "<>", "!=", "<", "<=", ">", ">="} {
		for _, pair := range [][2]string{{"a", "b"}, {"range_merge(a,a)", "b"}, {"a", "range_merge(b,b)"}} {
			for _, qualified := range []bool{false, true} {
				token := op
				if qualified {
					token = "OPERATOR(pg_catalog." + op + ")"
				}
				q := "SELECT /*é*/ " + pair[0] + " " + token + " " + pair[1] + " AS v FROM r"
				statements, err := Parse(q)
				if err != nil || len(statements) != 1 {
					t.Fatal(q, statements, err)
				}
				target := statements[0].(*Select).Targets[0]
				node, ok := target.(OperatorExpr)
				if !ok || len(node.Args) != 2 {
					t.Fatalf("%s: %#v", q, target)
				}
				want := op
				if op == "!=" {
					want = "<>"
				}
				if node.Name.Parts[len(node.Name.Parts)-1].Text != want {
					t.Fatal("operator spelling", q, node)
				}
				at := strings.Index(q, " "+token+" ") + 1
				position := utf8.RuneCountInString(q[:at]) + 1
				if node.Pos.Character != position {
					t.Fatal("character position", q, node.Pos.Character, position)
				}
			}
		}
	}
	statements, err := Parse("SELECT a + b * c FROM r")
	if err != nil {
		t.Fatal(err)
	}
	root := statements[0].(*Select).Targets[0].(OperatorExpr)
	if root.Name.Parts[0].Text != "+" || root.Args[1].(OperatorExpr).Name.Parts[0].Text != "*" {
		t.Fatal("set/arithmetic precedence", root)
	}
}
