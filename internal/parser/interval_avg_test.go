package parser

import (
	"errors"
	"stuntgres/internal/sqlstate"
	"testing"
)

func TestIntervalAvgGrammar(t *testing.T) {
	for _, q := range []string{"SELECT avg(v) FROM t", "SELECT pg_catalog.avg(v) FROM t", "SELECT \"avg\"(v) FROM t", "SELECT avg(),avg(*),avg(v,v) FROM t", "SELECT avg(avg(v)) FROM t", "SELECT k FROM t GROUP BY k HAVING avg(v)=INTERVAL '1 day'", "SELECT avg($1::interval) FROM t"} {
		statements, err := Parse(q)
		if err != nil || len(statements) != 1 {
			t.Fatal(q, statements, err)
		}
	}
	statements, err := Parse("SELECT /*é*/ pg_catalog.avg(v) FROM t")
	if err != nil {
		t.Fatal(err)
	}
	call, ok := statements[0].(*Select).Targets[0].(AggregateExpr)
	if !ok || call.Name != "avg" || call.Schema == nil || call.Schema.Text != "pg_catalog" || call.Pos.Character != 14 || call.Star || len(call.Args) != 1 {
		t.Fatal(call)
	}
}

func TestIntervalAvgExclusions(t *testing.T) {
	statements, err := Parse("SELECT avg(INTERVAL '1 day')")
	if err != nil || len(statements) != 1 || statements[0].(*Select).From != nil {
		t.Fatal(statements, err)
	}
	for _, q := range []string{"SELECT avg(v) OVER () FROM t"} {
		statements, err := Parse(q)
		var d *sqlstate.Error
		if len(statements) != 0 || !errors.As(err, &d) || d.Code != "0A000" {
			t.Fatal(q, statements, err)
		}
	}
}
