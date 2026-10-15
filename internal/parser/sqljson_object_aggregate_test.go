package parser

import (
	"strings"
	"testing"
	"unicode/utf8"
)

func TestSQLJSONObjectAggregatePositionedOptions(t *testing.T) {
	const q = "/*é😀*/ SELECT json_objectagg($1::text:$2::bytea FORMAT JSON ENCODING UTF8 ABSENT ON NULL WITH UNIQUE KEYS RETURNING bytea FORMAT JSON ENCODING UTF8) FILTER(WHERE flag) FROM t"
	statements, err := Parse(q)
	if err != nil || len(statements) != 1 {
		t.Fatal(err)
	}
	call, ok := statements[0].(*Select).Targets[0].(AggregateExpr)
	if !ok || call.Name != "json_objectagg" || len(call.Args) != 2 || call.JSON == nil || call.Filter == nil {
		t.Fatal("pair or filter lost", call)
	}
	options := call.JSON
	if !options.Absent || !options.Unique || options.Returning == nil || options.Returning.Name != "bytea" || options.Input == nil || options.Output == nil || options.Input.Encoding != "UTF8" || options.Output.Encoding != "UTF8" {
		t.Fatal("options lost", options)
	}
	for _, tc := range []struct {
		pos   Position
		token string
	}{
		{call.Pos, "json_objectagg"}, {options.Input.Pos, "FORMAT JSON ENCODING UTF8 ABSENT"},
		{options.Input.EncodingPos, "UTF8 ABSENT"}, {options.NullPos, "ABSENT"},
		{options.UniquePos, "WITH UNIQUE"}, {options.Returning.Pos, "bytea FORMAT"},
		{options.Output.Pos, "FORMAT JSON ENCODING UTF8)"},
	} {
		want := utf8.RuneCountInString(q[:strings.LastIndex(q, tc.token)]) + 1
		if tc.pos.Character != want {
			t.Fatal(tc.token, tc.pos, want)
		}
	}
	for _, q := range []string{"SELECT json_objectagg('a':1)", "SELECT json_objectagg('a' VALUE 1)", "SELECT json_objectagg((1+2) VALUE 3)", "SELECT json_objectagg(('a'::text) VALUE 1)"} {
		statements, err := Parse(q)
		if err != nil {
			t.Fatal(q, err)
		}
		call := statements[0].(*Select).Targets[0].(AggregateExpr)
		if call.JSON == nil || call.JSON.Absent || call.JSON.Unique || call.JSON.UniquePos.Character != 0 {
			t.Fatal("object defaults", q, call)
		}
	}
	for _, q := range []string{
		"CREATE TABLE json_objectagg(value text,format text,keys text)",
		"SELECT json_objectagg, value, format, keys FROM json_objectagg",
		"SELECT json_objectagg(value VALUE format FORMAT JSON WITH UNIQUE KEYS) FROM json_objectagg",
		"SELECT json_objectagg('a':json_arrayagg(v FORMAT JSON) WITH UNIQUE), format FROM t",
		"SELECT json_arrayagg(json_objectagg('a':v WITH UNIQUE) FORMAT JSON), keys FROM t",
		"SELECT json_objectagg('a':1 WITHOUT UNIQUE), value FROM t",
		`SELECT json_objectagg('a':'{"x":1,"x":2}'::json WITH UNIQUE)`,
		`SELECT json_objectagg('a':'{"x":1,"x":2}'::json WITHOUT UNIQUE KEYS)`,
	} {
		if statements, err := Parse(q); err != nil || len(statements) != 1 {
			t.Fatal("context scope leak", q, err)
		}
	}
}
