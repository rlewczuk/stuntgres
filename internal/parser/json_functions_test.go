package parser

import (
	"strings"
	"testing"
)

func TestJSONFunctionCallAST(t *testing.T) {
	for _, name := range []string{"json_typeof", "jsonb_typeof", "json_array_length", "jsonb_array_length", "json_object_keys", "jsonb_object_keys", "json_each", "json_each_text", "jsonb_each", "jsonb_each_text", "json_array_elements", "json_array_elements_text", "jsonb_array_elements", "jsonb_array_elements_text"} {
		for _, tc := range []struct {
			written, want       string
			qualified, variadic bool
		}{
			{name, name, false, false},
			{strings.ToUpper(name), name, false, false},
			{`"` + strings.ToUpper(name) + `"`, strings.ToUpper(name), false, false},
			{"pg_catalog." + name, name, true, false},
			{"pg_catalog." + name, name, true, true},
		} {
			arg := "$1"
			if tc.variadic {
				arg = "VARIADIC $1"
			}
			q := "/*é*/ SELECT " + tc.written + "(" + arg + ")"
			statements, err := Parse(q)
			if err != nil || len(statements) != 1 {
				t.Fatal(q, err)
			}
			call, ok := statements[0].(*Select).Targets[0].(FunctionExpr)
			wantParts := 1
			if tc.qualified {
				wantParts = 2
			}
			if !ok || len(call.Name.Parts) != wantParts || call.Name.Parts[wantParts-1].Text != tc.want || len(call.Args) != 1 || call.Variadic != tc.variadic || call.Pos.Character != 14 || call.Pos.Offset != 14 || call.Syntax != OrdinaryFunctionSyntax {
				t.Fatal(q, call)
			}
			if parameter, ok := call.Args[0].(ParameterExpr); !ok || parameter.Number != 1 {
				t.Fatal("argument expanded", call)
			}
		}
	}
}

func TestJSONFunctionParserConsumersAndDecorations(t *testing.T) {
	for _, query := range []string{
		`SELECT json_typeof FROM t`,
		`SELECT 1 AS json_object_keys`,
		`CREATE TABLE jsonb_typeof(json_array_length text)`,
		`SELECT json_typeof()`,
		`SELECT json_typeof(*)`,
		`SELECT json_typeof(a => NULL)`,
		`SELECT json_typeof(a := NULL)`,
		`SELECT json_typeof(DISTINCT '{}')`,
		`SELECT json_typeof('{}' ORDER BY 1)`,
		`SELECT json_typeof('{}') FILTER (WHERE true)`,
		`SELECT json_typeof('{}') OVER ()`,
		`SELECT * FROM json_object_keys($1) WITH ORDINALITY AS k(v,n)`,
		`SELECT * FROM ROWS FROM(json_object_keys($1),string_to_table('x',','),regexp_matches('q1','q([0-9])','g')) WITH ORDINALITY AS r(k,s,m,n)`,
		`SELECT * FROM pg_catalog.jsonb_object_keys(VARIADIC $1)`,
		`SELECT id,k FROM t,LATERAL json_object_keys(j) AS k`,
		`SELECT upper(json_object_keys('{"a":1}'))`,
		`SELECT CASE WHEN false THEN json_object_keys('{}') ELSE 'x' END`,
		`CREATE TABLE t(j json,k text DEFAULT json_typeof('null'),g int GENERATED ALWAYS AS (json_array_length(j)) STORED,CHECK(json_typeof(j)='array'))`,
		`CREATE INDEX ix ON t(json_array_length(j))`,
		`CREATE VIEW v AS SELECT jsonb_object_keys(b) FROM t`,
	} {
		if _, err := Parse(query); err != nil {
			t.Fatal(query, err)
		}
	}
	for _, name := range []string{"json_build_object", "jsonb_object"} {
		if JSONFunctionName(name) {
			t.Fatal("promoted adjacent capability", name)
		}
	}
}
