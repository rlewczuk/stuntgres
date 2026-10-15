package parser

import "testing"

func TestJSONStripNullsCallSyntax(t *testing.T) {
	for _, tc := range []struct {
		call, name string
		arity      int
	}{
		{`json_strip_nulls($1)`, "json_strip_nulls", 1}, {`JSON_STRIP_NULLS($1,$2)`, "json_strip_nulls", 2},
		{`pg_catalog.json_strip_nulls($1,$2)`, "json_strip_nulls", 2}, {`"JSON_STRIP_NULLS"($1)`, "JSON_STRIP_NULLS", 1},
	} {
		statements, err := Parse("/*é*/ SELECT " + tc.call)
		if err != nil || len(statements) != 1 {
			t.Fatal(tc, err)
		}
		call, ok := statements[0].(*Select).Targets[0].(FunctionExpr)
		if !ok || call.Name.Parts[len(call.Name.Parts)-1].Text != tc.name || len(call.Args) != tc.arity || call.Pos.Character != 14 {
			t.Fatal(tc, call)
		}
	}
	for _, op := range []string{"=>", ":="} {
		statements, err := Parse("SELECT json_strip_nulls(strip_in_arrays " + op + " $1,target " + op + " $2)")
		if err != nil {
			t.Fatal(err)
		}
		call := statements[0].(*Select).Targets[0].(FunctionExpr)
		for i, name := range []string{"strip_in_arrays", "target"} {
			if call.ArgNames[i].Text != name || call.Args[i].(ParameterExpr).Number != i+1 {
				t.Fatal(call)
			}
		}
	}
	for _, q := range []string{
		`SELECT json_strip_nulls(*)`, `SELECT json_strip_nulls(DISTINCT '{}')`, `SELECT json_strip_nulls('{}' ORDER BY 1)`,
		`SELECT json_strip_nulls('{}') FILTER (WHERE true)`, `SELECT json_strip_nulls('{}') OVER ()`,
		`SELECT json_strip_nulls('{}',VARIADIC ARRAY[true])`,
		`SELECT json_strip_nulls(target => '{}'::json,VARIADIC strip_in_arrays => true)`,
		`SELECT json_strip_nulls(strip_in_arrays => true,VARIADIC target => '{}'::json)`,
		`CREATE TABLE t(v json DEFAULT json_strip_nulls('{}'),CHECK(json_strip_nulls(v)::text='{}'))`,
		`CREATE VIEW v AS SELECT json_strip_nulls(strip_in_arrays => true,target => '{}')`,
	} {
		if _, err := Parse(q); err != nil {
			t.Fatal(q, err)
		}
	}
	if JSONFunctionName("jsonb_strip_nulls") {
		t.Fatal("adjacent JSONB function admitted")
	}
}
