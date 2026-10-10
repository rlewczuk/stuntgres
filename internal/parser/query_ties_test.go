package parser

import (
	"strconv"
	"strings"
	"stuntgres/internal/sqlstate"
	"testing"
)

func TestQueryTiesGrammar(t *testing.T) {
	for _, q := range []string{
		"SELECT k FROM t ORDER BY k FETCH FIRST 2 ROWS WITH TIES",
		"SELECT k FROM t ORDER BY k OFFSET 1 FETCH NEXT ROW WITH TIES",
		"SELECT k FROM t ORDER BY k FETCH NEXT (1+1) ROWS WITH TIES OFFSET 1",
		"(SELECT k FROM t ORDER BY k) FETCH FIRST ROW WITH TIES",
		"VALUES(1),(1) ORDER BY 1 FETCH FIRST ROW WITH TIES",
	} {
		statements, err := Parse(q)
		if err != nil || len(statements) != 1 {
			t.Fatal(q, statements, err)
		}
		var bounds *QueryBounds
		if s, ok := statements[0].(*Select); ok {
			bounds = s.Bounds
		}
		if bounds == nil || !bounds.WithTies || bounds.Count == nil || bounds.WithPosition.Offset != strings.Index(q, "WITH TIES") {
			t.Fatal(q, bounds)
		}
	}
}

func TestQueryTiesDiagnosticNoPrefix(t *testing.T) {
	for _, q := range []string{
		"SELECT k FROM t FETCH FIRST ROW WITH TIES",
		"SELECT k FROM (SELECT k FROM t ORDER BY k) AS a FETCH FIRST ROW WITH TIES",
		"SELECT k FROM t ORDER BY k FOR UPDATE SKIP LOCKED FOR UPDATE NOWAIT FETCH FIRST ROW WITH TIES",
	} {
		prefix := "INSERT INTO t VALUES(9);"
		statements, err := Parse(prefix + q)
		d, ok := err.(*sqlstate.Error)
		message := "WITH TIES cannot be specified without ORDER BY clause"
		if strings.Contains(q, "SKIP LOCKED") {
			message = "SKIP LOCKED and WITH TIES options cannot be used together"
		}
		if statements != nil || !ok || d.Code != "42601" || d.Message != message || d.Position != strconv.Itoa(strings.Index(prefix+q, "WITH TIES")+1) {
			t.Fatal(statements, err)
		}
	}
}
