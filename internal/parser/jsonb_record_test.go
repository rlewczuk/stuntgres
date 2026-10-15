package parser

import (
	"strings"
	"testing"
	"unicode/utf8"
)

func TestJSONBRecordParser(t *testing.T) {
	for _, source := range []string{
		`jsonb_to_record($1) AS (a integer,b text COLLATE "C",c numeric(4,1),d integer[][])`,
		`jsonb_to_record($1) r(a integer,b text COLLATE "C",c numeric(4,1),d integer[][])`,
		`jsonb_to_record($1) AS r(a integer,b text COLLATE "C",c numeric(4,1),d integer[][])`,
		`ROWS FROM(jsonb_to_record($1) AS (a integer,b text COLLATE "C",c numeric(4,1),d integer[][])) WITH ORDINALITY AS r(a,b,c,d,n)`,
	} {
		query := "/*é🙂*/ SELECT * FROM " + source
		statements, err := Parse(query)
		if err != nil || len(statements) != 1 {
			t.Fatal(query, err)
		}
		item := statements[0].(*Select).From
		var definitions []FunctionColumnDefinition
		if alias, ok := item.(*AliasReference); ok {
			definitions = alias.ColumnDefinitions
			item = alias.Input
		}
		function, ok := item.(*FunctionTable)
		if !ok || len(function.Calls) != 1 {
			t.Fatal("missing function source", item)
		}
		if definitions == nil {
			definitions = function.Calls[0].ColumnDefinitions
		}
		if len(definitions) != 4 {
			t.Fatal("lost complete definition list", definitions)
		}
		for i, name := range []string{"a", "b", "c", "d"} {
			d := definitions[i]
			offset := strings.Index(query, name+" ")
			if d.Name.Text != name || d.Name.Pos.Offset != offset || d.Name.Pos.Character != utf8.RuneCountInString(query[:offset])+1 {
				t.Fatal("member position", d)
			}
		}
		if definitions[0].Type.Name != "integer" || definitions[1].Type.Name != "text" || definitions[2].Type.Name != "numeric" || definitions[3].Type.Name != "integer" {
			t.Fatal("tail types lost", definitions)
		}
		if definitions[1].Collation == nil || definitions[1].Collation.Parts[0].Text != "C" {
			t.Fatal("collation lost")
		}
		modifiers := definitions[2].Type.Modifiers
		if len(modifiers) != 2 || modifiers[0].Integer == nil || *modifiers[0].Integer != 4 || modifiers[1].Integer == nil || *modifiers[1].Integer != 1 || len(definitions[3].Type.ArrayBounds) != 2 {
			t.Fatal("tail modifiers/rank lost", definitions)
		}
	}
	for _, name := range []string{"jbr_missing_function", "public.jbr_missing_function"} {
		query := "/*é🙂*/ SELECT * FROM ROWS FROM(" + name + "(1),generate_series(1,2)) AS r(a int)"
		statements, err := Parse(query)
		if err != nil || len(statements) != 1 {
			t.Fatal("missing-call resolution was hidden by parser rejection", query, err)
		}
		alias := statements[0].(*Select).From.(*AliasReference)
		source := alias.Input.(*FunctionTable)
		if len(source.Calls) != 2 || len(alias.ColumnDefinitions) != 1 || source.Calls[0].Name.Parts[len(source.Calls[0].Name.Parts)-1].Text != "jbr_missing_function" {
			t.Fatal("lost diagnostic call or definitions", source, alias)
		}
	}
	for _, name := range []string{"jsonb_populate_record", "jsonb_populate_recordset", "jsonb_to_record", "jsonb_to_recordset", "jsonb_populate_record_valid"} {
		if !JSONBRecordFunctionName(name) || !JSONFunctionName(name) {
			t.Fatal("selected syntax missing", name)
		}
	}
}
