package parser

import "testing"

func TestJSONObjectCallAST(t *testing.T) {
	for _, tc := range []struct {
		call, name   string
		parts, arity int
		variadic     bool
	}{
		{`json_object('{a,1}')`, "json_object", 2, 1, false},
		{`JSON_OBJECT('{a,1}')`, "json_object", 2, 1, false},
		{`"json_object"('{a,1}')`, "json_object", 1, 1, false},
		{`"JSON_OBJECT"('{a,1}')`, "JSON_OBJECT", 1, 1, false},
		{`pg_catalog.json_object('{a,1}')`, "json_object", 2, 1, false},
		{`pg_catalog.json_object(VARIADIC $1)`, "json_object", 2, 1, true},
		{`pg_catalog.json_object($1, VARIADIC $2)`, "json_object", 2, 2, true},
	} {
		query := "/*é*/ SELECT " + tc.call
		statements, err := Parse(query)
		if err != nil || len(statements) != 1 {
			t.Fatal(query, err)
		}
		selectNode := statements[0].(*Select)
		call, ok := selectNode.Targets[0].(FunctionExpr)
		if !ok || call.Name.Parts[len(call.Name.Parts)-1].Text != tc.name || len(call.Name.Parts) != tc.parts || len(call.Args) != tc.arity || call.Variadic != tc.variadic || call.Syntax != OrdinaryFunctionSyntax || call.Pos.Character != 14 || call.Pos.Offset != 14 {
			t.Fatal(query, call)
		}
		if tc.parts == 2 && call.Name.Parts[0].Text != "pg_catalog" {
			t.Fatal(call)
		}
		if tc.variadic {
			for i, arg := range call.Args {
				parameter, ok := arg.(ParameterExpr)
				if !ok || parameter.Number != i+1 {
					t.Fatal("expanded final array", call)
				}
			}
		}
	}
}

func TestJSONObjectParserConsumersAndBoundaries(t *testing.T) {
	for _, query := range []string{
		"SELECT json_object FROM t",
		"SELECT 1 AS json_object",
		"CREATE TABLE json_object(json_object text[])",
		"CREATE TABLE t(a text[],j json DEFAULT json_object('{a,1}'),g json GENERATED ALWAYS AS (json_object(a)) STORED,CHECK(json_object(a)::text <> '{}'))",
		"CREATE INDEX ix ON t ((json_object(a)::text))",
		"CREATE VIEW v AS SELECT json_object(a) FROM t",
		"INSERT INTO t VALUES(pg_catalog.json_object(VARIADIC $1))",
		"SELECT * FROM json_object('{a,1}')",
		"SELECT json_object()",
		"SELECT json_object('a' VALUE '1')",
		"SELECT json_object(KEY 'a' VALUE '1')",
	} {
		if _, err := Parse(query); err != nil {
			t.Fatal(query, err)
		}
	}
	for _, query := range []string{
		"SELECT json_object('a':1) trailing garbage",
		"SELECT json_object('a':1 WITH UNIQUE KEYS) garbage suffix",
	} {
		if _, err := Parse(query); err == nil {
			t.Fatal("admitted adjacent capability", query)
		}
	}
	for _, name := range []string{"json_objectagg"} {
		if binaryScalarFunction(name) {
			t.Fatal("promoted adjacent provider", name)
		}
	}
	if !IdentifierNeedsQuotes("json_object") {
		t.Fatal("lost deparse keyword quoting")
	}
}
