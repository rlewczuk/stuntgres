package parser

import "testing"

func TestJSONBSetCallSyntax(t *testing.T) {
	for _, tc := range []struct {
		call, name string
		arity      int
		variadic   bool
	}{
		{`jsonb_set($1,$2,$3)`, "jsonb_set", 3, false},
		{`JSONB_SET($1,$2,$3,$4)`, "jsonb_set", 4, false},
		{`"jsonb_set"($1,$2,$3)`, "jsonb_set", 3, false},
		{`"JSONB_SET"($1,$2,$3)`, "JSONB_SET", 3, false},
		{`pg_catalog.jsonb_set($1,$2,VARIADIC $3)`, "jsonb_set", 3, true},
		{`jsonb_set_lax($1,$2,$3,$4,$5)`, "jsonb_set_lax", 5, false},
	} {
		statements, err := Parse("/*é*/ SELECT " + tc.call)
		if err != nil || len(statements) != 1 {
			t.Fatal(tc, err)
		}
		call, ok := statements[0].(*Select).Targets[0].(FunctionExpr)
		if !ok || call.Name.Parts[len(call.Name.Parts)-1].Text != tc.name || len(call.Args) != tc.arity || call.Variadic != tc.variadic || call.Pos.Character != 14 {
			t.Fatal(tc, call)
		}
		for i, arg := range call.Args {
			parameter, ok := arg.(ParameterExpr)
			if !ok || parameter.Number != i+1 {
				t.Fatal("parameter expanded or reordered", tc, arg)
			}
		}
	}
	for _, op := range []string{"=>", ":="} {
		q := "SELECT jsonb_set_lax(replacement " + op + " $1,jsonb_in " + op + " $2,path " + op + " $3,null_value_treatment " + op + " $4)"
		statements, err := Parse(q)
		if err != nil {
			t.Fatal(err)
		}
		call := statements[0].(*Select).Targets[0].(FunctionExpr)
		for i, name := range []string{"replacement", "jsonb_in", "path", "null_value_treatment"} {
			if call.ArgNames[i].Text != name || call.Args[i].(ParameterExpr).Number != i+1 {
				t.Fatal(call)
			}
		}
	}
	for _, name := range []string{"jsonb_set", "pg_catalog.jsonb_set", "jsonb_set_lax", "pg_catalog.jsonb_set_lax"} {
		statements, err := Parse("SELECT " + name + "(*)")
		if err != nil {
			t.Fatal(name, err)
		}
		call, ok := statements[0].(*Select).Targets[0].(FunctionExpr)
		if !ok || !call.Star || len(call.Args) != 0 {
			t.Fatal("star must reach ordinary signature binding", name, call)
		}
	}
}

func TestJSONBSetStoredSyntaxAndAdjacentBoundaries(t *testing.T) {
	for _, q := range []string{
		`CREATE TABLE t(src jsonb,d jsonb DEFAULT jsonb_set('{}','{a}','2'),g jsonb GENERATED ALWAYS AS (jsonb_set_lax(src,'{a}',NULL,true,'use_json_null')) STORED,CHECK(jsonb_set(src,'{a}','2')=g))`,
		`CREATE INDEX ix ON t ((jsonb_set(src,'{a}','2')))`,
		`CREATE VIEW v AS SELECT jsonb_set_lax(src,'{a}',NULL,null_value_treatment => 'return_target') AS changed FROM t`,
		`INSERT INTO t(src) VALUES(jsonb_set('{}','{a}','2'))`,
		`SELECT jsonb_set FROM t`,
	} {
		if _, err := Parse(q); err != nil {
			t.Fatal(q, err)
		}
	}
	for _, name := range []string{"jsonb_insert", "jsonb_delete_path", "jsonb_path_set"} {
		if binaryScalarFunction(name) || JSONBSetFunctionName(name) {
			t.Fatal("adjacent function admitted", name)
		}
	}
}
