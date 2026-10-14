package parser

import "testing"

func TestJSONBObjectCallAST(t *testing.T) {
	for _, tc := range []struct {
		call, name   string
		parts, arity int
		variadic     bool
	}{
		{`jsonb_object('{a,1}')`, "jsonb_object", 1, 1, false},
		{`JSONB_OBJECT('{a,1}')`, "jsonb_object", 1, 1, false},
		{`"jsonb_object"('{a,1}')`, "jsonb_object", 1, 1, false},
		{`"JSONB_OBJECT"('{a,1}')`, "JSONB_OBJECT", 1, 1, false},
		{`pg_catalog.jsonb_object('{a,1}')`, "jsonb_object", 2, 1, false},
		{`pg_catalog.jsonb_object(VARIADIC $1)`, "jsonb_object", 2, 1, true},
		{`pg_catalog.jsonb_object($1, VARIADIC $2)`, "jsonb_object", 2, 2, true},
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

func TestJSONBObjectParserConsumersAndBoundaries(t *testing.T) {
	for _, query := range []string{
		"SELECT jsonb_object FROM t",
		"SELECT 1 AS jsonb_object",
		"CREATE TABLE jsonb_object(jsonb_object text[])",
		"CREATE TABLE t(a text[],j jsonb DEFAULT jsonb_object('{a,1}'),g jsonb GENERATED ALWAYS AS (jsonb_object(a)) STORED,CHECK(jsonb_object(a)::text <> '{}'))",
		"CREATE INDEX ix ON t ((jsonb_object(a)::text))",
		"CREATE VIEW v AS SELECT jsonb_object(a) FROM t",
		"INSERT INTO t VALUES(pg_catalog.jsonb_object(VARIADIC $1))",
		"SELECT * FROM jsonb_object('{a,1}')",
	} {
		if _, err := Parse(query); err != nil {
			t.Fatal(query, err)
		}
	}
	for _, query := range []string{
		"SELECT jsonb_object('a' VALUE '1')",
		"SELECT jsonb_object(KEY 'a' VALUE '1')",
	} {
		if _, err := Parse(query); err == nil {
			t.Fatal("admitted adjacent capability", query)
		}
	}
	for _, name := range []string{"json_objectagg", "jsonb_objectagg"} {
		if binaryScalarFunction(name) {
			t.Fatal("promoted adjacent provider", name)
		}
	}
	if IdentifierNeedsQuotes("jsonb_object") {
		t.Fatal("ordinary identifier unexpectedly needs quoting")
	}
}
