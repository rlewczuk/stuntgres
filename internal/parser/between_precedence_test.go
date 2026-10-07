package parser

import "testing"

func TestBetweenComparisonPrecedence(t *testing.T) {
	statements, err := Parse("SELECT TRUE BETWEEN 1<2 AND 2<3 FROM t")
	if err != nil {
		t.Fatal(err)
	}
	outer, ok := statements[0].(*Select).Targets[0].(OperatorExpr)
	if !ok || outer.Name.Parts[0].Text != "<" {
		t.Fatal(statements[0])
	}
	branch, ok := outer.Args[0].(LogicalExpr)
	if !ok || branch.Op != LogicalAnd {
		t.Fatal(outer)
	}
	if branch.Pos.Character != 13 {
		t.Fatal("BETWEEN opener lost", branch.Pos)
	}
	lower := branch.Left.(OperatorExpr).Args[1].(OperatorExpr)
	upper := branch.Right.(OperatorExpr).Args[1].(LiteralExpr)
	if lower.Name.Parts[0].Text != "<" || upper.Value.Integer != 2 || outer.Args[1].(LiteralExpr).Value.Integer != 3 {
		t.Fatal(outer)
	}
}

func TestBetweenGroupedComparisonEndpoints(t *testing.T) {
	for _, query := range []string{
		"SELECT TRUE BETWEEN (1<2) AND (2<3) FROM t",
		"SELECT TRUE BETWEEN 1<2 AND (2<3) FROM t",
		"DELETE FROM t WHERE TRUE BETWEEN 1<2 AND (2<3)",
	} {
		statements, err := Parse(query)
		if err != nil {
			t.Fatal(query, err)
		}
		var expression Expr
		switch statement := statements[0].(type) {
		case *Select:
			expression = statement.Targets[0]
		case *Delete:
			expression = statement.Where
		}
		branch, ok := expression.(LogicalExpr)
		if !ok || branch.Op != LogicalAnd {
			t.Fatal(query, expression)
		}
		for _, child := range []Expr{branch.Left, branch.Right} {
			endpoint, ok := child.(OperatorExpr).Args[1].(OperatorExpr)
			if !ok || endpoint.Name.Parts[0].Text != "<" || endpoint.Args[0].(LiteralExpr).Value.Integer+1 != endpoint.Args[1].(LiteralExpr).Value.Integer {
				t.Fatal(query, child)
			}
		}
	}
}

func TestBetweenPreservesOrdinaryGroupedComparisonBoundary(t *testing.T) {
	for _, query := range []string{
		"SELECT (id=1)=TRUE FROM t",
		"SELECT (v IS NULL)=TRUE FROM t",
		"SELECT TRUE=(v IS NULL) FROM t",
		"DELETE FROM t WHERE (v IS NULL)=TRUE",
		"SELECT CASE WHEN (v IS NULL)=TRUE THEN 1 ELSE 0 END FROM t",
		"CREATE TABLE d(b BOOL DEFAULT ((1=1)=TRUE))",
	} {
		if statements, err := Parse(query); err == nil || len(statements) != 0 {
			t.Fatal("ordinary grouped comparison scope expanded", query, statements, err)
		}
	}
	for _, query := range []string{
		"SELECT (id=1) BETWEEN FALSE AND TRUE FROM t",
		"SELECT (v IS NULL) BETWEEN FALSE AND TRUE FROM t",
		"SELECT 2 BETWEEN 1 AND 3 = TRUE FROM t",
		"SELECT TRUE = 2 BETWEEN 1 AND 3 FROM t",
	} {
		if _, err := Parse(query); err != nil {
			t.Fatal("BETWEEN comparison/grouping rejected", query, err)
		}
	}
}
