package parser

import (
	"strings"
	"testing"
	"unicode/utf8"
)

func TestJSONElementsFormalAndColumnDefinitionAST(t *testing.T) {
	for _, name := range []string{"json_array_elements", "json_array_elements_text"} {
		for _, notation := range []string{"=>", ":="} {
			query := "/*é🙂*/ SELECT pg_catalog." + name + "(VARIADIC from_json " + notation + " $1)"
			statements, err := Parse(query)
			if err != nil || len(statements) != 1 {
				t.Fatal(query, statements, err)
			}
			call, ok := statements[0].(*Select).Targets[0].(FunctionExpr)
			offset := strings.Index(query, "pg_catalog")
			if !ok || len(call.Name.Parts) != 2 || call.Name.Parts[1].Text != name || !call.Variadic || len(call.ArgNames) != 1 || call.ArgNames[0].Text != "from_json" || len(call.Args) != 1 || call.Pos.Offset != offset || call.Pos.Character != utf8.RuneCountInString(query[:offset])+1 {
				t.Fatal("lost selected call syntax", call)
			}
			if parameter, ok := call.Args[0].(ParameterExpr); !ok || parameter.Number != 1 {
				t.Fatal("lost parameter", call)
			}
		}
		query := "/*é🙂*/ SELECT * FROM " + name + "('[]') AS j(v json)"
		statements, err := Parse(query)
		if err != nil || len(statements) != 1 {
			t.Fatal(query, statements, err)
		}
		alias, ok := statements[0].(*Select).From.(*AliasReference)
		offset := strings.Index(query, "v json")
		if !ok || len(alias.ColumnDefinitions) != 1 || len(alias.Columns) != 0 || alias.ColumnDefinitions[0].Name.Text != "v" || alias.ColumnDefinitions[0].Type.Name != "json" || alias.ColumnDefinitions[0].Name.Pos.Offset != offset || alias.ColumnDefinitions[0].Name.Pos.Character != utf8.RuneCountInString(query[:offset])+1 {
			t.Fatal("lost scalar definition position", alias)
		}
	}
}
