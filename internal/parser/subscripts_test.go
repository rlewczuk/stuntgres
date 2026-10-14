package parser

import "testing"

func TestSubscriptsParserAdmission(t *testing.T) {
	for _, tc := range []struct {
		call, name string
		arity      int
	}{
		{"generate_subscripts('{1,2}'::integer[],1)", "generate_subscripts", 2},
		{"generate_subscripts($1::integer[],$2,$3)", "generate_subscripts", 3},
		{"GENERATE_SUBSCRIPTS('{1,2}'::integer[],1)", "generate_subscripts", 2},
		{`"GENERATE_SUBSCRIPTS"('{1,2}'::integer[],1)`, "GENERATE_SUBSCRIPTS", 2},
		{"pg_catalog.generate_subscripts('{1,2}'::integer[],1)", "generate_subscripts", 2},
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
		if !ok || len(call.Args) != tc.arity || call.Name.Parts[len(call.Name.Parts)-1].Text != tc.name || call.Pos.Character != 14 || call.Syntax != OrdinaryFunctionSyntax {
			t.Fatal(call)
		}
	}
	statements, err := Parse("SELECT generate_subscripts FROM t")
	if err != nil {
		t.Fatal(err)
	}
	if _, ok := statements[0].(*Select).Targets[0].(ColumnExpr); !ok {
		t.Fatal("bare name became function")
	}
	if statements, err := Parse("SELECT generate_subscripts(ARRAY[1,2],1)+10"); err != nil || len(statements) != 1 {
		t.Fatal("array/wrapper admission", statements, err)
	}
}

func TestSubscriptsParserDecorations(t *testing.T) {
	for _, tc := range []struct {
		call  string
		check func(FunctionExpr) bool
	}{
		{"generate_subscripts(ALL '{1}'::integer[],1)", func(e FunctionExpr) bool { return len(e.Args) == 2 && !e.Distinct }},
		{"generate_subscripts(DISTINCT '{1}'::integer[],1)", func(e FunctionExpr) bool { return e.Distinct }},
		{"generate_subscripts('{1}'::integer[],1 ORDER BY 1)", func(e FunctionExpr) bool { return len(e.Order) == 1 }},
		{"generate_subscripts('{1}'::integer[],1) FILTER(WHERE true)", func(e FunctionExpr) bool { return e.Filter != nil }},
		{"generate_subscripts('{1}'::integer[],1) OVER()", func(e FunctionExpr) bool { return e.Window != nil }},
		{"generate_subscripts(a=>'{1}'::integer[],dim=>1)", func(e FunctionExpr) bool { return len(e.ArgNames) == 2 }},
		{"generate_subscripts(VARIADIC ARRAY[1,2])", func(e FunctionExpr) bool { return e.Variadic }},
		{"generate_subscripts(*)", func(e FunctionExpr) bool { return e.Star }},
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

func TestSubscriptsParserRestrictions(t *testing.T) {
	// Context owners receive actual call trees; parser admission is not FROM
	// execution eligibility, nor permission to expand INSERT VALUES natively.
	for _, q := range []string{
		"SELECT 1 WHERE generate_subscripts('{1}'::integer[],1)>0",
		"CREATE TABLE t(v integer DEFAULT generate_subscripts('{1}'::integer[],1))",
		"CREATE TABLE t(v integer CHECK(generate_subscripts('{1}'::integer[],1)>0))",
		"SELECT s FROM generate_subscripts('{1}'::integer[],1) g(s)",
		"SELECT row_number() OVER(ORDER BY generate_subscripts('{1}'::integer[],1))",
		"INSERT INTO t VALUES(generate_subscripts('{1}'::integer[],1))",
	} {
		if statements, err := Parse(q); err != nil || len(statements) != 1 {
			t.Fatal(q, statements, err)
		}
	}
}
