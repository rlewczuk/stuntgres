package parser

import (
	"errors"
	"strconv"
	"strings"
	"testing"
	"unicode/utf8"

	"stuntgres/internal/sqlstate"
)

func TestSRFFromParser(t *testing.T) {
	for _, wrapper := range []string{"abs", "ceil", "pg_catalog.abs"} {
		statements, err := Parse("/*é🙂*/ SELECT * FROM " + wrapper + "(generate_series(1,2))")
		if err != nil || len(statements) != 1 {
			t.Fatal(wrapper, statements, err)
		}
		source, ok := statements[0].(*Select).From.(*FunctionTable)
		if !ok || len(source.Calls) != 1 || source.Calls[0].Pos.Character != 22 {
			t.Fatal("wrapper AST/position", source)
		}
		nested, ok := source.Calls[0].Args[0].(FunctionExpr)
		if !ok || nested.Name.Parts[0].Text != "generate_series" || len(nested.Args) != 2 {
			t.Fatal("lost lexical child", source.Calls)
		}
	}
	statements, err := Parse("SELECT * FROM LATERAL ROWS FROM(generate_series(1,2),generate_subscripts('{1}'::integer[],1)) WITH ORDINALITY AS g(v,i,n)")
	if err != nil {
		t.Fatal(err)
	}
	alias := statements[0].(*Select).From.(*AliasReference)
	source := alias.Input.(*FunctionTable)
	if !source.Lateral || !source.RowsFrom || !source.Ordinality || len(source.Calls) != 2 || alias.Name.Text != "g" || len(alias.Columns) != 3 {
		t.Fatal(alias, source)
	}
	for _, sql := range []string{
		"SELECT * FROM generate_series(1,2) AS g(v integer)",
		"SELECT * FROM ROWS FROM(generate_series(1,2),generate_series(3,4)) WITH ORDINALITY AS r(v integer)",
		"SELECT * FROM generate_series(1,2) WITH ORDINALITY AS g(v integer)",
		"SELECT * FROM regexp_split_to_table('a,b',',') AS g(v text)",
	} {
		for _, prefix := range []string{"", "/*é🙂*/ "} {
			input := prefix + sql
			statements, err := Parse(input)
			if err != nil || len(statements) != 1 {
				t.Fatal(input, statements, err)
			}
			alias := statements[0].(*Select).From.(*AliasReference)
			offset := strings.Index(input, "(v ") + 1
			if !alias.HasColumnDefinitions || len(alias.Columns) != 1 || alias.Columns[0].Text != "v" || alias.Columns[0].Pos.Offset != offset || alias.Columns[0].Pos.Character != utf8.RuneCountInString(input[:offset])+1 {
				t.Fatal("lost column definition name/position", input, alias)
			}
			name := "integer"
			if strings.Contains(sql, "v text") {
				name = "text"
			}
			if alias.ColumnDefinitionType.Name != name || alias.ColumnDefinitionType.Pos.Offset <= offset {
				t.Fatal("lost column definition type syntax", input, alias.ColumnDefinitionType)
			}
		}
	}
	for _, sql := range []string{
		"SELECT * FROM t AS g(v integer)",
		"SELECT * FROM generate_series(1,2) AS g(v integer,)",
	} {
		if statements, err := Parse(sql); err == nil || statements != nil {
			t.Fatal("admitted malformed/non-function column definitions", sql, statements, err)
		}
	}
	for _, tc := range []struct {
		sql, message, hint string
		position           int
	}{
		{"SELECT * FROM generate_series(1,2) WITH nope", `syntax error at or near "WITH"`, "", 36},
	} {
		for _, prefix := range []string{"", "/*é🙂*/ "} {
			statements, err := Parse(prefix + tc.sql)
			var diagnostic *sqlstate.Error
			position := tc.position
			if prefix != "" {
				position += 7
			}
			want := sqlstate.Error{Severity: "ERROR", SeverityUnlocalized: "ERROR", Code: "42601", Message: tc.message, Hint: tc.hint, Position: strconv.Itoa(position)}
			if statements != nil || !errors.As(err, &diagnostic) || *diagnostic != want {
				t.Fatalf("%s: %#v %v; want %#v", prefix+tc.sql, diagnostic, err, want)
			}
		}
	}
}
