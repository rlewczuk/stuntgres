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

func TestBetweenGroupedComparisonConsumption(t *testing.T) {
	for _, query := range []string{
		"SELECT (id=1)=TRUE FROM t",
		"SELECT (v IS NULL)=TRUE FROM t",
		"SELECT TRUE=(v IS NULL) FROM t",
		"DELETE FROM t WHERE (v IS NULL)=TRUE",
		"SELECT CASE WHEN (v IS NULL)=TRUE THEN 1 ELSE 0 END FROM t",
		"CREATE TABLE d(b BOOL DEFAULT ((1=1)=TRUE))",
	} {
		if statements, err := Parse(query); err != nil || len(statements) != 1 {
			t.Fatal("grouped comparison rejected", query, statements, err)
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
	for _, query := range []string{"SELECT id<2<3 FROM t", "SELECT (id<2)+1 FROM t"} {
		if _, err := Parse(query); err == nil {
			t.Fatal("nonassociative/arithmetic boundary lost", query)
		}
	}
}

func TestGroupedPredicateFilterComparisonConsumers(t *testing.T) {
	for _, tc := range []struct {
		input     string
		predicate PredicateKind
		logical   LogicalOperator
		isLogical bool
	}{
		{input: "v IS NULL", predicate: IsNull},
		{input: "v IS NOT NULL", predicate: IsNotNull},
		{input: "v IS TRUE", predicate: IsTrue},
		{input: "v IS NOT TRUE", predicate: IsNotTrue},
		{input: "v IS FALSE", predicate: IsFalse},
		{input: "v IS NOT FALSE", predicate: IsNotFalse},
		{input: "v IS UNKNOWN", predicate: IsUnknown},
		{input: "v IS NOT UNKNOWN", predicate: IsNotUnknown},
		{input: "v IS DISTINCT FROM 1", predicate: IsDistinct},
		{input: "v IS NOT DISTINCT FROM 1", predicate: IsNotDistinct},
		{input: "NOT (v IS NULL)", logical: LogicalNot, isLogical: true},
		{input: "v IS NULL AND id=1", logical: LogicalAnd, isLogical: true},
		{input: "v IS NULL OR id=1", logical: LogicalOr, isLogical: true},
	} {
		for _, prefix := range []string{"SELECT id FROM t WHERE ", "DELETE FROM t WHERE "} {
			for _, right := range []bool{false, true} {
				expression := "(" + tc.input + ")=TRUE"
				operandIndex := 0
				if right {
					expression = "TRUE=(" + tc.input + ")"
					operandIndex = 1
				}
				query := prefix + expression
				statements, err := Parse(query)
				if err != nil || len(statements) != 1 {
					t.Fatal(query, statements, err)
				}
				var filter Expr
				switch statement := statements[0].(type) {
				case *Select:
					filter = statement.Where
				case *Delete:
					filter = statement.Where
				default:
					t.Fatal(query, statements[0])
				}
				comparison, ok := filter.(OperatorExpr)
				if !ok || len(comparison.Name.Parts) != 1 || comparison.Name.Parts[0].Text != "=" || len(comparison.Args) != 2 {
					t.Fatal(query, filter)
				}
				operand := comparison.Args[operandIndex]
				if tc.isLogical {
					logical, ok := operand.(LogicalExpr)
					if !ok || logical.Op != tc.logical {
						t.Fatal(query, operand)
					}
				} else {
					predicate, ok := operand.(PredicateExpr)
					if !ok || predicate.Kind != tc.predicate {
						t.Fatal(query, operand)
					}
				}
			}
		}
	}
	for _, query := range []string{
		"DELETE FROM t WHERE id<2<3",
		"DELETE FROM t WHERE (v IS NULL)+1=2",
		"DELETE FROM t WHERE id BETWEEN 1<2<3 AND TRUE",
	} {
		if _, err := Parse(query); err == nil {
			t.Fatal("excluded chain/arithmetic/lower bound admitted", query)
		}
	}
}
