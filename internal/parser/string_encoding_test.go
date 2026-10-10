package parser

import (
	"strings"
	"testing"
)

func TestStringEncodingParserAdmission(t *testing.T) {
	for _, tc := range []struct {
		call, name string
		arity      int
	}{
		{"ascii('é')", "ascii", 1}, {"chr(65)", "chr", 1}, {"to_ascii('é','LATIN1')", "to_ascii", 2},
		{`unistr($$\0041$$)`, "unistr", 1}, {"unicode_assigned('A')", "unicode_assigned", 1},
		{"ASCII('A')", "ascii", 1}, {`"ASCII"('A')`, "ASCII", 1},
		{"ascii()", "ascii", 0}, {"chr(1,2)", "chr", 2},
	} {
		for _, prefix := range []string{"", "pg_catalog."} {
			statements, err := Parse("/*é🙂*/ SELECT " + prefix + tc.call)
			if err != nil {
				t.Fatal(tc, prefix, err)
			}
			selected, ok := statements[0].(*Select)
			if !ok || selected.From != nil {
				t.Fatal("singleton selected admission", statements)
			}
			call, ok := selected.Targets[0].(FunctionExpr)
			if !ok || len(call.Args) != tc.arity || call.Name.Parts[len(call.Name.Parts)-1].Text != tc.name || call.Syntax != OrdinaryFunctionSyntax || call.StringSyntax != OrdinaryStringSyntax || call.Pos.Character != 15 || call.ResolutionOnly {
				t.Fatal("ordinary selected identity/provenance", tc, prefix, call)
			}
			if prefix != "" && (len(call.Name.Parts) != 2 || call.Name.Parts[0].Text != "pg_catalog") {
				t.Fatal("qualified name lost", call)
			}
		}
	}
	statements, err := Parse("SELECT unicode_assigned(chr(888)),ascii(unistr($$\0041$$))")
	if err != nil {
		t.Fatal(err)
	}
	selected := statements[0].(*Select)
	for i, name := range []string{"chr", "unistr"} {
		outer, ok := selected.Targets[i].(FunctionExpr)
		if !ok {
			t.Fatal(selected.Targets[i])
		}
		child, ok := outer.Args[0].(FunctionExpr)
		if !ok || child.Name.Parts[0].Text != name {
			t.Fatal("nested scalar call", outer)
		}
	}
	for _, query := range []string{
		"CREATE TABLE se_parser(s text DEFAULT chr(65),u text DEFAULT unistr($$\0041$$),a text DEFAULT to_ascii('é','LATIN1'))",
		"CREATE TABLE se_parser(s text,n int,a int GENERATED ALWAYS AS (ascii(s)) STORED,c text GENERATED ALWAYS AS (chr(n)) STORED,CHECK(unicode_assigned(s)))",
		"CREATE INDEX se_parser_index ON se_parser ((to_ascii(s,'LATIN1')))",
		"SELECT ascii(s),chr(n),to_ascii(s,'LATIN1'),unistr(s),unicode_assigned(s) FROM se_parser",
		"INSERT INTO se_parser(s) VALUES(chr(65)),(unistr($$\0041$$))",
	} {
		if _, err := Parse(query); err != nil {
			t.Fatal("existing scalar production", query, err)
		}
	}
}

func TestStringEncodingParserIdentifierBoundaries(t *testing.T) {
	names := []string{"ascii", "chr", "to_ascii", "unistr", "unicode_assigned"}
	statements, err := Parse("SELECT " + strings.Join(names, ",") + " FROM se_parser")
	if err != nil {
		t.Fatal(err)
	}
	selected := statements[0].(*Select)
	for i, name := range names {
		column, ok := selected.Targets[i].(ColumnExpr)
		if !ok || len(column.Name.Parts) != 1 || column.Name.Parts[0].Text != name {
			t.Fatal("bare identifier became function", name, selected.Targets[i])
		}
		if _, err := Parse("CREATE TABLE " + name + "(" + name + " text)"); err != nil {
			t.Fatal("ordinary table/column identifier", name, err)
		}
		if !stringEncodingFunction(strings.ToUpper(name)) || stringEncodingFunction(name+"_other") {
			t.Fatal("scoped admission classification", name)
		}
	}
	if _, err := Parse("SELECT encoding_unrelated(1)"); err == nil {
		t.Fatal("unrelated function capability widened")
	}
}
