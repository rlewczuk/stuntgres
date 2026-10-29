package parser

import (
	"strings"
	"testing"
)

func TestJSONBPathQueryTZParser(t *testing.T) {
	for _, name := range []string{"jsonb_path_exists_tz", "jsonb_path_match_tz", "jsonb_path_query_tz", "jsonb_path_query_array_tz", "jsonb_path_query_first_tz"} {
		if !JSONBPathQueryFunctionName(name) || !JSONBPathQueryFunctionName(strings.ToUpper(name)) || JSONBPathQueryFunctionName(name+"_tz") || JSONBPathQueryFunctionName(name+"_extra") {
			t.Fatal("unbounded timezone syntax", name)
		}
		for _, spelling := range []string{name, "pg_catalog." + name, `"` + strings.ToUpper(name) + `"`} {
			statements, err := Parse("/*é*/ SELECT " + spelling + "(path => $1,target => $2,vars => $3,VARIADIC silent => $4)")
			if err != nil || len(statements) != 1 {
				t.Fatal(spelling, err)
			}
			call, ok := statements[0].(*Select).Targets[0].(FunctionExpr)
			if !ok || len(call.Args) != 4 || !call.Variadic || call.Pos.Character != 14 {
				t.Fatal("lost timezone call transport", call)
			}
			for i, label := range []string{"path", "target", "vars", "silent"} {
				if call.ArgNames[i].Text != label || call.Args[i].(ParameterExpr).Number != i+1 {
					t.Fatal("lost written order", call)
				}
			}
		}
		statements, err := Parse("SELECT " + name + "(DISTINCT $1,$2 ORDER BY $3) FILTER (WHERE true) OVER ()")
		if err != nil {
			t.Fatal(err)
		}
		call := statements[0].(*Select).Targets[0].(FunctionExpr)
		if !call.Distinct || len(call.Order) != 1 || call.Filter == nil || call.Window == nil {
			t.Fatal("discarded modifiers", call)
		}
		if _, err := Parse("SELECT " + name + "($1,$2) unsupported trailing tokens"); err == nil {
			t.Fatal("accepted valid prefix")
		}
	}
	for _, q := range []string{
		`SELECT * FROM pg_catalog.jsonb_path_query_tz($1,$2) WITH ORDINALITY AS j(v,n)`,
		`SELECT * FROM ROWS FROM(jsonb_path_query_tz($1,$2),generate_series(1,2)) AS j(v,n)`,
		`CREATE TABLE t(j jsonb DEFAULT jsonb_path_query_first_tz('"2023-08-15"','$.timestamp_tz()'),CHECK(jsonb_path_exists_tz(j,'$')))`,
		`CREATE VIEW v AS SELECT jsonb_path_query_tz(j,'$') FROM t`,
		`SELECT jsonb_path_exists_tz('{}','$') AND jsonb_path_match_tz('true','$')`,
		`SELECT COALESCE(jsonb_path_query_array_tz('[]','$'),jsonb_path_query_first_tz('true','$'))`,
	} {
		if _, err := Parse(q); err != nil {
			t.Fatal(q, err)
		}
	}
}
