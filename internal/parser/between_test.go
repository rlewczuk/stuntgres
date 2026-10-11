package parser

import (
	"reflect"
	"testing"
)

func TestBetweenLowering(t *testing.T) {
	for _, tc := range []struct {
		form      string
		ops       []string
		logic     LogicalOperator
		outer     LogicalOperator
		symmetric bool
	}{
		{"BETWEEN", []string{">=", "<="}, LogicalAnd, LogicalOr, false},
		{"NOT BETWEEN", []string{"<", ">"}, LogicalOr, LogicalAnd, false},
		{"BETWEEN SYMMETRIC", []string{">=", "<="}, LogicalAnd, LogicalOr, true},
		{"NOT BETWEEN SYMMETRIC", []string{"<", ">"}, LogicalOr, LogicalAnd, true},
	} {
		ast, err := Parse("SELECT /*é*/ a " + tc.form + " l AND u FROM t")
		if err != nil {
			t.Fatal(tc.form, err)
		}
		node := ast[0].(*Select).Targets[0].(LogicalExpr)
		if node.Pos != (Position{Offset: 16, Character: 16}) {
			t.Fatal(node.Pos)
		}
		branches := []Expr{node}
		if tc.symmetric {
			if node.Op != tc.outer {
				t.Fatal(node)
			}
			branches = []Expr{node.Left, node.Right}
		}
		for index, raw := range branches {
			branch := raw.(LogicalExpr)
			if branch.Op != tc.logic || branch.Pos != node.Pos {
				t.Fatal(branch)
			}
			bounds := []string{"l", "u"}
			if index == 1 {
				bounds = []string{"u", "l"}
			}
			for i, child := range []Expr{branch.Left, branch.Right} {
				comparison := child.(OperatorExpr)
				if comparison.Name.Parts[0].Text != tc.ops[i] || comparison.Pos != node.Pos || comparison.Name.Parts[0].Pos != node.Pos {
					t.Fatal(comparison)
				}
				if comparison.Args[0].(ColumnExpr).Name.Parts[0].Text != "a" || comparison.Args[1].(ColumnExpr).Name.Parts[0].Text != bounds[i] {
					t.Fatal(comparison)
				}
				if comparison.Args[0].SourcePosition() != (Position{Offset: 14, Character: 14}) {
					t.Fatal(comparison.Args[0])
				}
				if reflect.DeepEqual(comparison.Args[1].SourcePosition(), comparison.Pos) {
					t.Fatal("operand position replaced")
				}
			}
		}
	}
}

func TestBetweenGrammarBoundaries(t *testing.T) {
	for _, q := range []string{
		"SELECT TRUE BETWEEN (FALSE OR TRUE) AND TRUE FROM t",
		"SELECT TRUE BETWEEN 1<2 AND 2<3 FROM t",
		"SELECT TRUE BETWEEN (1<2) AND (2<3) FROM t",
		"SELECT TRUE BETWEEN 1<2 AND (2<3) FROM t",
		"SELECT 2 BETWEEN 1 AND 3 = TRUE FROM t",
		"DELETE FROM t WHERE 2 BETWEEN 1 AND 3 = TRUE",
		"SELECT CASE WHEN 2 BETWEEN 1 AND 3 = TRUE THEN TRUE ELSE FALSE END FROM t",
		"SELECT TRUE BETWEEN (1 BETWEEN 0 AND 2) AND TRUE FROM t",
		"SELECT TRUE BETWEEN FALSE AND (2 BETWEEN 1 AND 3) FROM t",
		"SELECT NOT v BETWEEN 1 AND 3 FROM t WHERE v NOT BETWEEN 1 AND 3 AND v=0",
		"DELETE FROM t WHERE t.v BETWEEN SYMMETRIC (1+0) AND 3",
		"DELETE FROM t WHERE v BETWEEN 1 AND 3",
		"DELETE FROM t WHERE v BETWEEN SYMMETRIC 3 AND 1",
		"DELETE FROM t WHERE v NOT BETWEEN 1 AND 3",
		"DELETE FROM t WHERE v NOT BETWEEN SYMMETRIC 3 AND 1",
		"SELECT CASE WHEN v BETWEEN 1 AND 3 THEN TRUE ELSE FALSE END FROM t",
		"CREATE TABLE t(b BOOL DEFAULT (2 BETWEEN 1 AND 3))",
	} {
		if _, err := Parse(q); err != nil {
			t.Fatal(q, err)
		}
	}
	for _, q := range []string{
		"SELECT v BETWEEN 1 3 FROM t", "SELECT v BETWEEN AND 3 FROM t", "SELECT v BETWEEN 1 AND",
		"SELECT v BETWEEN 1 AND 3 BETWEEN 0 AND 4 FROM t", "SELECT v BETWEEN TRUE OR FALSE AND TRUE FROM t",
		"SELECT v BETWEEN ASYMMETRIC 1 AND 3 FROM t", "CREATE TABLE t(b BOOL DEFAULT 2 BETWEEN 1 AND 3)",
		"SELECT v BETWEEN 1 AND 3 FROM t; garbage",
	} {
		if ast, err := Parse(q); err == nil || len(ast) != 0 {
			t.Fatal("accepted excluded syntax", q, ast, err)
		}
	}
}

func TestBetweenDeleteCursorBoundary(t *testing.T) {
	statements, err := Parse("DELETE FROM t WHERE CURRENT OF c")
	if err != nil || len(statements) != 1 || statements[0].(*Delete).CurrentOf.Text != "c" || statements[0].(*Delete).Where != nil {
		t.Fatal("positioned DELETE qualifier rejected", statements, err)
	}
	for _, q := range []string{`DELETE FROM t WHERE "current" OF c`, `DELETE FROM t WHERE CURRENT "of" c`} {
		if statements, err := Parse(q); err == nil || len(statements) != 0 {
			t.Fatal("quoted cursor words accepted", q, statements, err)
		}
	}
}
