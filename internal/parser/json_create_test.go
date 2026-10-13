package parser

import (
	"strings"
	"testing"
)

func TestJSONCreateParserAdmissionAndProvenance(t *testing.T) {
	for _, name := range []string{"to_json", "to_jsonb", "array_to_json", "row_to_json", "json_build_array", "jsonb_build_array", "json_build_object", "jsonb_build_object"} {
		for _, spelling := range []string{name, strings.ToUpper(name), `pg_catalog."` + name + `"`, `"` + strings.ToUpper(name) + `"`} {
			query := "/*é*/ SELECT " + spelling + "(VARIADIC $1) AS v"
			nodes, err := Parse(query)
			if err != nil {
				t.Fatal(query, err)
			}
			raw, ok := nodes[0].(*Select).Targets[0].(FunctionExpr)
			if !ok || !raw.Variadic || len(raw.Args) != 1 || raw.Syntax != OrdinaryFunctionSyntax || raw.Pos.Character != 14 || raw.Pos.Offset != 14 {
				t.Fatal("selected provenance lost", raw)
			}
			param, ok := raw.Args[0].(ParameterExpr)
			if !ok || param.Number != 1 || param.Pos.Character <= raw.Pos.Character {
				t.Fatal("argument position lost", param)
			}
			last := raw.Name.Parts[len(raw.Name.Parts)-1].Text
			if strings.HasPrefix(spelling, `"`) {
				if last != strings.ToUpper(name) {
					t.Fatal(last)
				}
			} else if last != name {
				t.Fatal(last)
			}
		}
	}
	for _, q := range []string{
		`SELECT to_json(DISTINCT 1)`, `SELECT to_json(1 ORDER BY 1)`,
		`SELECT to_json(1) FILTER(WHERE TRUE)`, `SELECT to_json(1) OVER()`,
		`SELECT json_build_array(*)`, `SELECT to_json(a => 1)`,
		`CREATE VIEW v AS SELECT row_to_json(ROW(1,'x'))`,
		`CREATE TABLE t(n int,j json DEFAULT to_json('x'::text),CHECK(json_build_array(n)::text <> '[]'))`,
		`CREATE INDEX i ON t ((to_json(n)::text))`,
	} {
		if _, err := Parse(q); err != nil {
			t.Fatal("lost ordinary function consumer", q, err)
		}
	}
	for _, name := range []string{"jsonb_object", "json_objectagg", "json_arrayagg", "json_serialize", "json_query"} {
		if jsonCreateFunction(name) {
			t.Fatal("adjacent JSON capability promoted", name)
		}
	}
}
