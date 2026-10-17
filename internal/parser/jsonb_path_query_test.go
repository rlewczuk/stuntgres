package parser

import (
	"strings"
	"testing"
)

func TestJSONBPathQueryParser(t *testing.T) {
	for _, name := range []string{"jsonb_path_exists", "jsonb_path_match", "jsonb_path_query_first", "jsonb_path_query"} {
		if !JSONBPathQueryFunctionName(strings.ToUpper(name)) || JSONBPathQueryFunctionName(name+"_tz") {
			t.Fatal("unbounded admission", name)
		}
		for _, spelling := range []string{name, "pg_catalog." + name, `"` + strings.ToUpper(name) + `"`} {
			statements, err := Parse("/*é*/ SELECT " + spelling + "(path => $1,target => $2,vars => $3,VARIADIC silent => $4)")
			if err != nil || len(statements) != 1 {
				t.Fatal(spelling, err)
			}
			call, ok := statements[0].(*Select).Targets[0].(FunctionExpr)
			if !ok || len(call.Args) != 4 || !call.Variadic || call.Pos.Character != 14 {
				t.Fatal("lost call transport", call)
			}
			for i, label := range []string{"path", "target", "vars", "silent"} {
				if call.ArgNames[i].Text != label || call.Args[i].(ParameterExpr).Number != i+1 {
					t.Fatal("reordered named argument", call)
				}
			}
		}
		statements, err := Parse("SELECT " + name + "(DISTINCT $1,$2 ORDER BY $3) FILTER (WHERE true) OVER ()")
		if err != nil {
			t.Fatal(name, err)
		}
		call := statements[0].(*Select).Targets[0].(FunctionExpr)
		if !call.Distinct || len(call.Order) != 1 || call.Filter == nil || call.Window == nil {
			t.Fatal("discarded modifiers", call)
		}
		if _, err := Parse("SELECT " + name + "($1,$2) unsupported trailing tokens"); err == nil {
			t.Fatal("accepted suffix", name)
		}
	}
	for _, q := range []string{
		`SELECT * FROM pg_catalog.jsonb_path_query($1,$2) WITH ORDINALITY AS j(v,n)`,
		`SELECT * FROM ROWS FROM (jsonb_path_query($1,$2),generate_series(1,2)) AS j(v,n)`,
		`CREATE TABLE t(j jsonb,b boolean DEFAULT jsonb_path_exists('{}','$'),f jsonb GENERATED ALWAYS AS (jsonb_path_query_first(j,'$')) STORED,CHECK(jsonb_path_match(j,'exists($)')))`,
		`CREATE INDEX ix ON t ((jsonb_path_query_first(j,'$')))`,
		`CREATE VIEW v AS SELECT jsonb_path_query(j,'$') FROM t`,
	} {
		if _, err := Parse(q); err != nil {
			t.Fatal(q, err)
		}
	}
}
