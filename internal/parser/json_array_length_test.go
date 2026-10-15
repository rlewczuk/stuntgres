package parser

import (
	"strings"
	"testing"
)

func TestJSONArrayLengthParserAdmission(t *testing.T) {
	for _, tc := range []struct {
		call, name string
		arity      int
	}{
		{`json_array_length('[]')`, "json_array_length", 1},
		{`JSONB_ARRAY_LENGTH($1)`, "jsonb_array_length", 1},
		{`"json_array_length"(NULL)`, "json_array_length", 1},
		{`"JSON_ARRAY_LENGTH"('[]')`, "JSON_ARRAY_LENGTH", 1},
		{`json_array_length()`, "json_array_length", 0},
		{`jsonb_array_length('[]','[]')`, "jsonb_array_length", 2},
	} {
		for _, prefix := range []string{"", "pg_catalog."} {
			statements, err := Parse("/*é🙂*/ SELECT " + prefix + tc.call)
			if err != nil {
				t.Fatal(tc, prefix, err)
			}
			selected, ok := statements[0].(*Select)
			if !ok || selected.From != nil {
				t.Fatal(statements)
			}
			call, ok := selected.Targets[0].(FunctionExpr)
			if !ok || len(call.Args) != tc.arity || call.Name.Parts[len(call.Name.Parts)-1].Text != tc.name || call.Syntax != OrdinaryFunctionSyntax || call.Pos.Character != 15 || call.ResolutionOnly {
				t.Fatal(tc, prefix, call)
			}
			if prefix != "" && (len(call.Name.Parts) != 2 || call.Name.Parts[0].Text != "pg_catalog") {
				t.Fatal("qualified identity lost", call)
			}
		}
	}
	for _, query := range []string{
		`CREATE TABLE jal_parser(j json,b jsonb,n int DEFAULT json_array_length('[1]'),g int GENERATED ALWAYS AS (jsonb_array_length(b)) STORED,CHECK(json_array_length(j)>=0))`,
		`CREATE INDEX jal_parser_idx ON jal_parser ((json_array_length(j)))`,
		`SELECT json_array_length(j),jsonb_array_length(b) FROM jal_parser`,
		`INSERT INTO jal_parser(n) VALUES(json_array_length('[1]'))`,
	} {
		if _, err := Parse(query); err != nil {
			t.Fatal(query, err)
		}
	}
}

func TestJSONArrayLengthParserIdentifierBoundaries(t *testing.T) {
	for _, name := range []string{"json_array_length", "jsonb_array_length"} {
		statements, err := Parse("SELECT " + name + " FROM jal_parser")
		if err != nil {
			t.Fatal(err)
		}
		column, ok := statements[0].(*Select).Targets[0].(ColumnExpr)
		if !ok || column.Name.Parts[0].Text != name {
			t.Fatal("identifier became function", statements)
		}
		if _, err := Parse("CREATE TABLE " + name + "(" + name + " int)"); err != nil {
			t.Fatal(err)
		}
		if !JSONFunctionName(strings.ToUpper(name)) || JSONFunctionName(name+"_other") {
			t.Fatal("unbounded name admission", name)
		}
	}
	for _, name := range []string{"json_array", "jsonb_array"} {
		if JSONFunctionName(name) {
			t.Fatal("adjacent function admitted", name)
		}
	}
}
