package parser

import (
	"errors"
	"stuntgres/internal/sqlstate"
	"testing"
)

func TestQueryAggregateGrammar(t *testing.T) {
	for _, q := range []string{
		"SELECT count(v),count(),count(1,2),pg_catalog.count(v) FROM t",
		"SELECT k,count(*) FROM t WHERE v>0 GROUP BY k,v+1 HAVING count(*)>1 ORDER BY k",
		"SELECT k FROM t GROUP BY 1", "SELECT k FROM t GROUP BY +1", "SELECT k FROM t GROUP BY (1)",
		`SELECT "count", "count"(*) FROM t GROUP BY "count"`,
		"INSERT INTO out SELECT k,count(*) FROM t GROUP BY k HAVING count(*)>1",
		"INSERT INTO out SELECT FROM t GROUP BY v HAVING TRUE",
	} {
		t.Run(q, func(t *testing.T) {
			statements, err := Parse(q)
			if err != nil || len(statements) != 1 {
				t.Fatal(statements, err)
			}
		})
	}
	statements, err := Parse("SELECT /*é*/ count(*) FROM t GROUP BY k HAVING count(*)>1")
	if err != nil {
		t.Fatal(err)
	}
	s := statements[0].(*Select)
	call := s.Targets[0].(AggregateExpr)
	if !call.Star || call.Name != "count" || call.Pos.Character != 14 || len(s.GroupBy) != 1 || s.Having == nil || s.From == nil {
		t.Fatal(s, call)
	}
}
func TestQueryAggregateExclusions(t *testing.T) {
	for _, q := range []string{
		"SELECT count(* ORDER BY v) FROM t", "SELECT count(*) OVER () FROM t",
		"SELECT k FROM t GROUP BY ROLLUP(k)", "SELECT k FROM t GROUP BY CUBE(k)",
		"SELECT k FROM t GROUP BY GROUPING SETS ((k))", "SELECT k FROM t GROUP BY ()",
		"SELECT k FROM t GROUP BY DISTINCT k", "SELECT k FROM t GROUP BY ALL k",
		"SELECT k FROM t GROUP BY k FETCH FIRST 1 ROW WITH TIES",
	} {
		t.Run(q, func(t *testing.T) {
			statements, err := Parse("INSERT INTO t VALUES(1);" + q)
			var d *sqlstate.Error
			if err == nil || len(statements) != 0 || !errors.As(err, &d) {
				t.Fatal(statements, err)
			}
		})
	}
	for _, q := range []string{"SELECT k FROM t GROUP BY", "SELECT k FROM t GROUP BY k,", "SELECT k FROM t HAVING"} {
		_, err := Parse(q)
		var d *sqlstate.Error
		if !errors.As(err, &d) || d.Code != "42601" {
			t.Fatal(q, err)
		}
	}
}

func TestQueryAggregateNoFromSyntax(t *testing.T) {
	for _, query := range []string{
		"SELECT count(*)+1,CAST('0' AS regclass)",
		"SELECT CAST(count(*) AS BIGINT),CAST('0' AS regclass)",
		"SELECT num_nulls(count(*)),CAST('0' AS regclass)",
	} {
		statements, err := Parse(query)
		if err != nil || len(statements) != 1 || statements[0].(*Select).From != nil || len(statements[0].(*Select).Targets) != 2 {
			t.Fatal(query, statements, err)
		}
	}
	// INSERT and SELECT share the same no-FROM query syntax.
	statements, err := Parse("INSERT INTO out SELECT count(*)")
	if err != nil || len(statements) != 1 || statements[0].(*Insert).Source.Select == nil {
		t.Fatal(statements, err)
	}
}
