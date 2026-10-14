package parser

import "testing"

func TestSeriesParserAdmission(t *testing.T) {
	for _, tc := range []struct {
		call, name string
		arity      int
	}{
		{"generate_series(1,3)", "generate_series", 2},
		{"generate_series($1,3::bigint,$2)", "generate_series", 3},
		{"GENERATE_SERIES(1,3)", "generate_series", 2},
		{`"GENERATE_SERIES"(1,3)`, "GENERATE_SERIES", 2},
	} {
		statements, err := Parse("/*é*/ SELECT " + tc.call)
		if err != nil {
			t.Fatal(tc, err)
		}
		selected, ok := statements[0].(*Select)
		if !ok {
			t.Fatal(statements)
		}
		call, ok := selected.Targets[0].(FunctionExpr)
		if !ok || len(call.Args) != tc.arity || call.Name.Parts[0].Text != tc.name || call.Pos.Character != 14 || call.Syntax != OrdinaryFunctionSyntax {
			t.Fatal(call)
		}
	}
	statements, err := Parse("SELECT generate_series FROM t")
	if err != nil {
		t.Fatal(err)
	}
	if _, ok := statements[0].(*Select).Targets[0].(ColumnExpr); !ok {
		t.Fatal("bare name became call")
	}
}

func TestSeriesParserDecorations(t *testing.T) {
	for _, tc := range []struct {
		call  string
		check func(FunctionExpr) bool
	}{
		{"generate_series(ALL 1,3)", func(e FunctionExpr) bool { return len(e.Args) == 2 && !e.Distinct }},
		{"generate_series(DISTINCT 1,3)", func(e FunctionExpr) bool { return e.Distinct }},
		{"generate_series(1,3 ORDER BY 1)", func(e FunctionExpr) bool { return len(e.Order) == 1 }},
		{"generate_series(1,3) FILTER(WHERE true)", func(e FunctionExpr) bool { return e.Filter != nil }},
		{"generate_series(1,3) OVER()", func(e FunctionExpr) bool { return e.Window != nil }},
		{"generate_series(start=>1,stop=>3)", func(e FunctionExpr) bool { return len(e.ArgNames) == 2 }},
		{"generate_series(VARIADIC ARRAY[1,3])", func(e FunctionExpr) bool { return e.Variadic }},
	} {
		statements, err := Parse("SELECT " + tc.call)
		if err != nil {
			t.Fatal(tc.call, err)
		}
		e, ok := statements[0].(*Select).Targets[0].(FunctionExpr)
		if !ok || !tc.check(e) {
			t.Fatal(tc.call, e)
		}
	}
}

func TestSeriesParserRestrictions(t *testing.T) {
	// These syntax units retain the calls for contextual binding. They do not
	// assert SQL execution or broaden the selected provider's FROM capability.
	for _, q := range []string{
		"SELECT 1 WHERE generate_series(1,2)>0",
		"DELETE FROM t RETURNING generate_series(1,2)",
		"CREATE TABLE t(v integer CHECK(generate_series(1,2)>0))",
		"SELECT v FROM generate_series(1,2) s(v)",
		"SELECT row_number() OVER(ORDER BY generate_series(1,2))",
	} {
		if statements, err := Parse(q); err != nil || len(statements) != 1 {
			t.Fatal(q, statements, err)
		}
	}
}
