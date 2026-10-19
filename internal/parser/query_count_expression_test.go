package parser

import "testing"

func TestCountExpressionArguments(t *testing.T) {
	for _, q := range []string{
		"SELECT count(v),count(),count(1,2) FROM t",
		"SELECT pg_catalog.count(v),pg_catalog.count(),pg_catalog.count(1,2) FROM t",
		"SELECT count(v IS NULL),count(v>1),count((v>1) OR v IS NULL) FROM t",
		"SELECT count(abs(v)),count(abs(v),1) FROM t",
		"SELECT v FROM t FETCH FIRST count(v) ROWS ONLY",
	} {
		statements, err := Parse(q)
		if err != nil || len(statements) != 1 {
			t.Fatal(q, statements, err)
		}
	}
	statements, err := Parse("SELECT count(v),count(),count(1,2) FROM t")
	if err != nil {
		t.Fatal(err)
	}
	s := statements[0].(*Select)
	for i, want := range []int{1, 0, 2} {
		call, ok := s.Targets[i].(AggregateExpr)
		if !ok || call.Star || call.Name != "count" || len(call.Args) != want {
			t.Fatal(i, call)
		}
	}
	// Function-cast spelling retains predicates until routine exact lookup.
	// The binder keeps the legacy cast boundary when no SQL routine wins.
	for _, q := range []string{"SELECT text(v IS NULL) FROM t", "SELECT pg_catalog.text(v>1) FROM t"} {
		statements, err := Parse(q)
		if err != nil {
			t.Fatal("lost deferred function-call operand", q, err)
		}
		if call, ok := statements[0].(*Select).Targets[0].(CastExpr); !ok || call.Syntax != FunctionCast {
			t.Fatal("lost function-cast spelling", q)
		}
	}
	for _, q := range []string{"SELECT text(v) FROM t", "SELECT pg_catalog.text(v) FROM t"} {
		if _, err := Parse(q); err != nil {
			t.Fatal("existing function cast rejected", q, err)
		}
	}
}
