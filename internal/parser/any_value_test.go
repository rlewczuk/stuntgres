package parser

import (
	"testing"
)

func TestAnyValueGrammar(t *testing.T) {
	for _, q := range []string{"SELECT any_value(v) FROM t", "SELECT pg_catalog.any_value(v) FROM t", "SELECT \"any_value\"(COALESCE(v,1)) FROM t", "SELECT any_value(),any_value(*),any_value(v,1) FROM t", "SELECT k FROM t GROUP BY k HAVING any_value(v)>0 ORDER BY any_value(v)", "SELECT any_value($1::integer) FROM t"} {
		statements, err := Parse(q)
		if err != nil || len(statements) != 1 {
			t.Fatal(q, statements, err)
		}
	}
	statements, err := Parse("SELECT /*é*/ pg_catalog.any_value(v) FROM t")
	if err != nil {
		t.Fatal(err)
	}
	call, ok := statements[0].(*Select).Targets[0].(AggregateExpr)
	if !ok || call.Name != "any_value" || call.Schema == nil || call.Schema.Text != "pg_catalog" || call.Pos.Character != 14 || call.Star || len(call.Args) != 1 {
		t.Fatal(call)
	}
}

func TestAnyValueExclusions(t *testing.T) {
	for _, q := range []string{"SELECT any_value(1)", "SELECT COALESCE(any_value(1),0)", "SELECT any_value(1),nextval('s')", "SELECT any_value(1),current_schema()"} {
		statements, err := Parse(q)
		if err != nil || len(statements) != 1 || statements[0].(*Select).From != nil {
			t.Fatal(q, statements, err)
		}
	}
	for _, q := range []string{"SELECT any_value(v) OVER () FROM t"} {
		statements, err := Parse(q)
		if len(statements) != 1 || err != nil || statements[0].(*Select).Targets[0].(AggregateExpr).Window == nil {
			t.Fatal(q, statements, err)
		}
	}
}
