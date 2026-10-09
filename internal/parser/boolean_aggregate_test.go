package parser

import (
	"errors"
	"stuntgres/internal/sqlstate"
	"testing"
)

func TestBooleanAggregateGrammar(t *testing.T) {
	for _, name := range []string{"bool_and", "bool_or", "every"} {
		for _, q := range []string{"SELECT " + name + "(b) FROM t", "SELECT pg_catalog." + name + "(b IS NULL) FROM t", "SELECT \"" + name + "\"(COALESCE(b,FALSE)) FROM t", "SELECT " + name + "()," + name + "(*)," + name + "(b,TRUE) FROM t", "SELECT k FROM t GROUP BY k HAVING " + name + "(b)"} {
			statements, err := Parse(q)
			if err != nil || len(statements) != 1 {
				t.Fatal(q, statements, err)
			}
		}
		statements, err := Parse("SELECT /*é*/ pg_catalog." + name + "(b) FROM t")
		if err != nil {
			t.Fatal(err)
		}
		call := statements[0].(*Select).Targets[0].(AggregateExpr)
		if call.Name != name || call.Schema == nil || call.Schema.Text != "pg_catalog" || call.Pos.Character != 14 || call.Star || len(call.Args) != 1 {
			t.Fatal(call)
		}
	}
}

func TestBooleanAggregateExclusions(t *testing.T) {
	for _, q := range []string{"SELECT bool_and(TRUE)", "SELECT every(TRUE),nextval('s')"} {
		statements, err := Parse(q)
		if err != nil || len(statements) != 1 || statements[0].(*Select).From != nil {
			t.Fatal(q, statements, err)
		}
	}
	for _, q := range []string{"SELECT bool_or(DISTINCT b) FROM t", "SELECT every(ALL b) FROM t", "SELECT bool_and(b ORDER BY b) FROM t", "SELECT bool_or(b) FILTER (WHERE FALSE) FROM t", "SELECT every(b) OVER () FROM t"} {
		statements, err := Parse("INSERT INTO t VALUES(TRUE);" + q)
		var d *sqlstate.Error
		if len(statements) != 0 || !errors.As(err, &d) || d.Code != "0A000" {
			t.Fatal(q, statements, err)
		}
	}
}
