package parser

import "testing"

func TestSumGrammar(t *testing.T) {
	for _, prefix := range []string{"", "pg_catalog.", "\"sum_schema\"."} {
		for _, tc := range []struct {
			suffix                 string
			args, keys             int
			star, distinct, filter bool
		}{
			{"()", 0, 0, false, false, false},
			{"(*)", 0, 0, true, false, false},
			{"(v)", 1, 0, false, false, false},
			{"(v,1)", 2, 0, false, false, false},
			{"(DISTINCT v ORDER BY v) FILTER(WHERE f)", 1, 1, false, true, true},
			{"(ALL v ORDER BY v DESC NULLS LAST)", 1, 1, false, false, false},
			{"(sum(v))", 1, 0, false, false, false},
		} {
			q := "SELECT " + prefix + "sum" + tc.suffix + " FROM t"
			statements, err := Parse(q)
			if err != nil || len(statements) != 1 {
				t.Fatal(q, statements, err)
			}
			call, ok := statements[0].(*Select).Targets[0].(AggregateExpr)
			if !ok || call.Name != "sum" || len(call.Args) != tc.args || len(call.Order) != tc.keys || call.Star != tc.star || call.Distinct != tc.distinct || (call.Filter != nil) != tc.filter {
				t.Fatal(q, call)
			}
		}
	}
	q := "SELECT sum(v) FILTER(WHERE f) OVER() FROM t"
	statements, err := Parse(q)
	if err != nil || len(statements) != 1 {
		t.Fatal(q, statements, err)
	}
	call, ok := statements[0].(*Select).Targets[0].(AggregateExpr)
	if !ok || call.Name != "sum" || len(call.Args) != 1 || call.Filter == nil || call.Window == nil || call.Window.Bare || call.Window.Name != nil || len(call.Window.Partition) != 0 || len(call.Window.Order) != 0 {
		t.Fatal("SUM window syntax not retained for binder analysis", q, call)
	}
	for _, q := range []string{"SELECT \"sum\"(1)", "SELECT sum(v) FROM t suffix_that_is_not_an_alias suffix", "SELECT sum(DISTINCT *) FROM t"} {
		statements, err := Parse(q)
		if q == "SELECT \"sum\"(1)" {
			if err != nil || len(statements) != 1 {
				t.Fatal(q, err)
			}
			continue
		}
		if err == nil || len(statements) != 0 {
			t.Fatal("whole-input restriction lost", q, statements, err)
		}
	}
}
