package parser

import "testing"

func TestArrayAggregateGrammar(t *testing.T) {
	for _, q := range []string{
		"SELECT array_agg(),array_agg(*),array_agg(v,id) FROM t",
		"SELECT array_agg(v),array_agg(v::integer[]) FROM t",
		"SELECT array_agg(DISTINCT v ORDER BY v) FILTER(WHERE v IS NULL) FROM t",
		"SELECT array_agg(ALL v ORDER BY id DESC NULLS LAST) FROM t",
		"SELECT array_agg(NULL::integer)",
		"SELECT array_agg(v) OVER () FROM t",
		"INSERT INTO t(v) VALUES(array_agg(1))",
	} {
		statements, err := Parse(q)
		if err != nil || len(statements) != 1 {
			t.Fatal(q, statements, err)
		}
	}
	statements, err := Parse("SELECT /*é*/ pg_catalog.array_agg(v ORDER BY id DESC NULLS LAST) FROM t")
	if err != nil {
		t.Fatal(err)
	}
	call := statements[0].(*Select).Targets[0].(AggregateExpr)
	if call.Name != "array_agg" || call.Schema == nil || call.Schema.Text != "pg_catalog" || call.Pos.Character != 14 || len(call.Args) != 1 || len(call.Order) != 1 || !call.Order[0].Desc || call.Order[0].Nulls != NullsLast {
		t.Fatal("positioned shared aggregate AST", call)
	}
}
