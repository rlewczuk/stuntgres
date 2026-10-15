package parser

import (
	"strings"
	"testing"
	"unicode/utf8"
)

func TestJSONRecordParser(t *testing.T) {
	for _, source := range []string{
		`json_to_record($1) AS r(a integer,b varchar(3),c public.child[],d text COLLATE "C")`,
		`json_to_record($1) r(a integer,b varchar(3),c public.child[],d text COLLATE "C")`,
		`json_to_record($1) AS (a integer,b varchar(3),c public.child[],d text COLLATE "C")`,
		`ROWS FROM(json_to_record($1) AS (a integer,b varchar(3),c public.child[],d text COLLATE "C"))`,
	} {
		q := "/*é🙂*/ SELECT * FROM " + source
		statements, err := Parse(q)
		if err != nil || len(statements) != 1 {
			t.Fatal(q, statements, err)
		}
		from := statements[0].(*Select).From
		var definitions []FunctionColumnDefinition
		if alias, ok := from.(*AliasReference); ok {
			if len(alias.Columns) != 0 {
				t.Fatal("typed members duplicated as aliases")
			}
			definitions = alias.ColumnDefinitions
		} else {
			definitions = from.(*FunctionTable).Calls[0].ColumnDefinitions
		}
		if len(definitions) != 4 {
			t.Fatal("lost member definitions", definitions)
		}
		for i, name := range []string{"a", "b", "c", "d"} {
			member := definitions[i]
			at := strings.Index(q, name+" ")
			if member.Name.Text != name || member.Name.Pos.Offset != at || member.Name.Pos.Character != utf8.RuneCountInString(q[:at])+1 || member.Type.Pos.Offset <= at {
				t.Fatal(member)
			}
		}
		if definitions[1].Type.Name != "varchar" || len(definitions[1].Type.Modifiers) != 1 || definitions[2].Type.Schema == nil || definitions[2].Type.Schema.Text != "public" || len(definitions[2].Type.ArrayBounds) != 1 || definitions[3].Collation == nil || definitions[3].Collation.Parts[0].Text != "C" {
			t.Fatal("lost later type/modifier/collation", definitions)
		}
	}
}
