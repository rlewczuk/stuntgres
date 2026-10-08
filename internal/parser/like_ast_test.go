package parser

import (
	"strings"
	"testing"
)

func TestLikeLoweredASTPositions(t *testing.T) {
	for _, tc := range []struct{ keyword, symbol string }{
		{"LIKE", "~~"}, {"NOT LIKE", "!~~"}, {"ILIKE", "~~*"}, {"NOT ILIKE", "!~~*"},
	} {
		q := "SELECT v " + tc.keyword + " p ESCAPE e FROM t"
		s := orderParse(t, q)
		op, ok := s.Targets[0].(OperatorExpr)
		if !ok || len(op.Name.Parts) != 1 || op.Name.Parts[0].Text != tc.symbol || len(op.Args) != 2 {
			t.Fatalf("%s: %#v", q, s.Targets[0])
		}
		pos := strings.Index(q, tc.keyword) + 1
		if op.Pos.Character != pos || op.Name.Parts[0].Pos.Character != pos {
			t.Fatal("operator source position", q, op)
		}
		escape, ok := op.Args[1].(FunctionExpr)
		if !ok || len(escape.Name.Parts) != 2 || escape.Name.Parts[0].Text != "pg_catalog" || escape.Name.Parts[1].Text != "like_escape" || len(escape.Args) != 2 || escape.Pos.Character != pos {
			t.Fatalf("ESCAPE lowering %s: %#v", q, op.Args[1])
		}
	}
}

func TestLikeKeywordGenericPrecedenceAST(t *testing.T) {
	for _, symbol := range []string{"~~", "OPERATOR(pg_catalog.~~)"} {
		for _, tc := range []struct {
			query  string
			nested int
		}{
			{"SELECT a LIKE b " + symbol + " c FROM t", 1},
			{"SELECT a " + symbol + " b LIKE c FROM t", 0},
		} {
			s := orderParse(t, tc.query)
			root, ok := s.Targets[0].(OperatorExpr)
			if !ok || len(root.Name.Parts) != 1 || root.Name.Parts[0].Text != "~~" || len(root.Args) != 2 {
				t.Fatalf("keyword root %s: %#v", tc.query, s.Targets[0])
			}
			child, ok := root.Args[tc.nested].(OperatorExpr)
			if !ok || child.Name.Parts[len(child.Name.Parts)-1].Text != "~~" || len(child.Args) != 2 {
				t.Fatalf("generic operator binding %s: %#v", tc.query, root.Args[tc.nested])
			}
			if strings.HasPrefix(symbol, "OPERATOR") && len(child.Name.Parts) != 2 {
				t.Fatal("qualified symbol lost namespace", child)
			}
		}
	}
}

func TestLikeMixedLogicalBetweenAST(t *testing.T) {
	for _, tc := range []struct{ expression, shape string }{
		{"NOT v LIKE 'a%'", "NOT(~~(v,literal))"},
		{"v LIKE 'a%' = FALSE", "=(~~(v,literal),literal)"},
		{"v LIKE 'a%' OR v LIKE 'b%' AND FALSE", "OR(~~(v,literal),AND(~~(v,literal),literal))"},
		{"NOT v LIKE 'a%' AND TRUE OR v LIKE 'b%'", "OR(AND(NOT(~~(v,literal)),literal),~~(v,literal))"},
		{"v LIKE 'a%' AND id BETWEEN 1 AND 2", "AND(~~(v,literal),AND(>=(id,literal),<=(id,literal)))"},
		{"(v LIKE 'a%') BETWEEN FALSE AND TRUE", "AND(>=(~~(v,literal),literal),<=(~~(v,literal),literal))"},
	} {
		s := orderParse(t, "SELECT "+tc.expression+" FROM t")
		if got := likePrecedenceShape(t, s.Targets[0]); got != tc.shape {
			t.Fatalf("%s: %s want %s", tc.expression, got, tc.shape)
		}
	}
	for _, q := range []string{
		"SELECT v LIKE 'a%' BETWEEN FALSE AND TRUE FROM t",
		"SELECT v BETWEEN 'a' AND 'z' LIKE 'a%' FROM t",
	} {
		if _, err := Parse(q); err == nil {
			t.Fatal("ungrouped same-level predicates accepted", q)
		}
	}
}

func likePrecedenceShape(t *testing.T, expression Expr) string {
	t.Helper()
	switch e := expression.(type) {
	case ColumnExpr:
		return e.Name.Parts[len(e.Name.Parts)-1].Text
	case LiteralExpr:
		return "literal"
	case OperatorExpr:
		args := make([]string, len(e.Args))
		for i, arg := range e.Args {
			args[i] = likePrecedenceShape(t, arg)
		}
		return e.Name.Parts[len(e.Name.Parts)-1].Text + "(" + strings.Join(args, ",") + ")"
	case LogicalExpr:
		if e.Op == LogicalNot {
			return "NOT(" + likePrecedenceShape(t, e.Left) + ")"
		}
		name := "AND"
		if e.Op == LogicalOr {
			name = "OR"
		}
		return name + "(" + likePrecedenceShape(t, e.Left) + "," + likePrecedenceShape(t, e.Right) + ")"
	default:
		t.Fatalf("unexpected precedence node %T", expression)
		return ""
	}
}
