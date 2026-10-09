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
			if selectStmt == nil || !selectStmt.Distinct || selectStmt.Pos.Character != 1 && selectStmt.Pos.Character != 17 {
				t.Fatal(selectStmt)
			}
		})
	}
	statements, err := Parse("SELECT a FROM t")
	if err != nil || statements[0].(*Select).Distinct {
		t.Fatal(statements, err)
	}
}

func TestQueryDistinctGrammarBoundaries(t *testing.T) {
	for _, q := range []string{"SELECT DISTINCT ON (a) a FROM t", "SELECT count(DISTINCT a) FROM t", "SELECT a FROM t GROUP BY DISTINCT a", "SELECT DISTINCT DISTINCT a FROM t", "SELECT DISTINCT FROM t"} {
		statements, err := Parse(q)
		var diagnostic *sqlstate.Error
		if err == nil || len(statements) != 0 || !errors.As(err, &diagnostic) {
			t.Fatal(q, statements, err)
		}
		if q == "SELECT DISTINCT FROM t" && (diagnostic.Code != "42601" || diagnostic.Message != `syntax error at or near "FROM"` || diagnostic.Position != "17") {
			t.Fatal(diagnostic)
		}
	}
	// DISTINCT does not expand the ordinary relationless scalar boundary.
	keywordDiagnostic(t, "SELECT DISTINCT 1", "0A000", "SELECT without FROM is unsupported", "1")
}
