package parser

import (
	"fmt"
	"testing"
)

func TestCompareOpsGrammarAndPositions(t *testing.T) {
	for _, op := range []string{"=", "<>", "!=", "<", "<=", ">", ">="} {
		for _, operands := range [][2]string{{"i", "j"}, {"1", "i"}, {"i", "$1"}, {"$1", "i"}, {"'a'", "'b'"}, {"NULL", "NULL"}, {"TRUE", "FALSE"}, {"TIME '01:00'", "TIME '02:00'"}} {
			q := fmt.Sprintf("SELECT %s %s %s FROM t WHERE %s %s %s", operands[0], op, operands[1], operands[1], op, operands[0])
			statements, err := Parse(q)
			if err != nil {
				t.Fatal(q, err)
			}
			s := statements[0].(*Select)
			for _, expr := range []Expr{s.Targets[0], s.Where} {
				raw, ok := expr.(OperatorExpr)
				canonical := op
				if op == "!=" {
					canonical = "<>"
				}
				if !ok || len(raw.Args) != 2 || raw.Name.Parts[0].Text != canonical {
					t.Fatal(q, expr)
				}
			}
		}
	}
	statements, err := Parse("SELECT /*é*/ i!=j FROM t")
	if err != nil {
		t.Fatal(err)
	}
	raw := statements[0].(*Select).Targets[0].(OperatorExpr)
	if raw.Pos.Character != 15 || raw.Name.Parts[0].Pos != raw.Pos {
		t.Fatal(raw)
	}
	for _, op := range []string{"<=", ">=", "<>", "!="} {
		l := &lexer{input: "/*é*/ " + op}
		var value sqlSymType
		l.scan(&value)
		if value.tok.raw != op || value.tok.pos.Character != 7 {
			t.Fatal(value.tok)
		}
	}
	if _, err := Parse("SELECT i /*a*/ <= /*b*/ j FROM t WHERE j<>i"); err != nil {
		t.Fatal(err)
	}
	for _, q := range []string{"SELECT i::INT<j FROM t", "SELECT (i)<(j) FROM t", "SELECT id FROM t WHERE (id)=1"} {
		if _, err := Parse(q); err != nil {
			t.Fatal(q, err)
		}
	}
}

func TestCompareOpsExcludedSyntax(t *testing.T) {
	for _, q := range []string{
		"SELECT i < = j FROM t", "SELECT i ! = j FROM t", "SELECT i < /*x*/ = j FROM t",
		"SELECT i<j<1 FROM t",
		"SELECT i<j x FROM t", "SELECT i<j", "SELECT i<j FROM t garbage extra", "SELECT i<j FROM t; garbage",
		"INSERT INTO t VALUES(1<2)", "SET x TO 1<2",
	} {
		if statements, err := Parse(q); err == nil || len(statements) != 0 {
			t.Fatal(q, statements, err)
		}
	}
	statements, err := Parse("SELECT i<j AS x FROM t")
	if err != nil || len(statements) != 1 {
		t.Fatal("supported target label", statements, err)
	}
	selectStmt := statements[0].(*Select)
	if selectStmt.TargetLabels[0].Text != "x" {
		t.Fatal("lost target label", selectStmt)
	}
	if _, ok := selectStmt.Targets[0].(OperatorExpr); !ok {
		t.Fatal("lost comparison", selectStmt)
	}
}
