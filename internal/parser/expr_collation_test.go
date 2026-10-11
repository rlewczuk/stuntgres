package parser

import (
	"strings"
	"testing"
	"unicode/utf8"
)

func TestExprCollationAnnotationAST(t *testing.T) {
	for _, q := range []string{`SELECT (a=b) COLLATE "C" FROM t`, `SELECT /*é*/ (a=b) COLLATE pg_catalog."C" FROM t`} {
		statements, err := Parse(q)
		if err != nil || len(statements) != 1 {
			t.Fatal(q, statements, err)
		}
		annotation, ok := statements[0].(*Select).Targets[0].(CollateExpr)
		if !ok {
			t.Fatal("parenthesized BOOL annotation did not retain AST", statements[0])
		}
		if annotation.Pos.Character != utf8.RuneCountInString(q[:strings.Index(q, "COLLATE")])+1 {
			t.Fatal("COLLATE position lost", annotation.Pos)
		}
		comparison, ok := annotation.Child.(OperatorExpr)
		if !ok || comparison.Name.Parts[len(comparison.Name.Parts)-1].Text != "=" {
			t.Fatal("annotation lost boolean child", annotation.Child)
		}
	}
	if statements, err := Parse(`SELECT (a=b) COLLATE "C" FROM t trailing garbage`); err == nil || statements != nil {
		t.Fatal("parser accepted a partial input", statements, err)
	}
}
