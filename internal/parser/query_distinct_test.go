package parser

import (
	"errors"
	"testing"

	"stuntgres/internal/sqlstate"
)

func TestQueryDistinctGrammar(t *testing.T) {
	for _, q := range []string{
		"SELECT DISTINCT 1 AS one FROM t", "SELECT DISTINCT * FROM t",
		"SELECT DISTINCT a AS x,b FROM t WHERE a>0 GROUP BY a,b HAVING count(*)>1 ORDER BY x LIMIT 2 OFFSET 1",
		"SELECT DISTINCT a FROM t FOR KEY SHARE",
		"INSERT INTO out SELECT DISTINCT a FROM t ORDER BY a",
		"INSERT INTO out SELECT DISTINCT 1",
	} {
		t.Run(q, func(t *testing.T) {
			statements, err := Parse(q)
			if err != nil || len(statements) != 1 {
				t.Fatal(statements, err)
			}
			var selectStmt *Select
			switch s := statements[0].(type) {
			case *Select:
				selectStmt = s
			case *Insert:
				selectStmt = s.Source.Select
			}
			if selectStmt == nil || selectStmt.Distinct == nil || selectStmt.Pos.Character != 1 && selectStmt.Pos.Character != 17 {
				t.Fatal(selectStmt)
			}
		})
	}
	statements, err := Parse("SELECT a FROM t")
	if err != nil || statements[0].(*Select).Distinct != nil {
		t.Fatal(statements, err)
	}
}

func TestQueryDistinctGrammarBoundaries(t *testing.T) {
	for _, q := range []string{"SELECT DISTINCT ON () a FROM t", "SELECT DISTINCT DISTINCT a FROM t", "SELECT DISTINCT FROM t"} {
		statements, err := Parse(q)
		var diagnostic *sqlstate.Error
		if err == nil || len(statements) != 0 || !errors.As(err, &diagnostic) {
			t.Fatal(q, statements, err)
		}
		if q == "SELECT DISTINCT FROM t" && (diagnostic.Code != "42601" || diagnostic.Message != `syntax error at or near "FROM"` || diagnostic.Position != "17") {
			t.Fatal(diagnostic)
		}
	}
	statements, err := Parse("SELECT a FROM t GROUP BY DISTINCT a")
	if err != nil || len(statements) != 1 {
		t.Fatal(statements, err)
	}
	s := statements[0].(*Select)
	if s.Distinct != nil || s.GroupBy == nil || !s.GroupBy.Distinct || s.GroupBy.All || len(s.GroupBy.Items) != 1 || s.GroupBy.Items[0].Kind != GroupExpression {
		t.Fatal(s.Distinct, s.GroupBy)
	}
	column, ok := s.GroupBy.Items[0].Expr.(ColumnExpr)
	if !ok || len(column.Name.Parts) != 1 || column.Name.Parts[0].Text != "a" {
		t.Fatal(s.GroupBy)
	}
}
