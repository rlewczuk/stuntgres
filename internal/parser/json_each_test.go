package parser

import (
	"strings"
	"testing"
	"unicode/utf8"
)

func TestJSONEachColumnDefinitionAST(t *testing.T) {
	for _, function := range []string{"json_each", "json_each_text"} {
		for _, prefix := range []string{"", "/*é🙂*/ "} {
			input := prefix + "SELECT * FROM " + function + "('{}') AS j(k text,v json,n int)"
			statements, err := Parse(input)
			if err != nil || len(statements) != 1 {
				t.Fatal(input, statements, err)
			}
			alias, ok := statements[0].(*Select).From.(*AliasReference)
			if !ok || !alias.HasColumnDefinitions || len(alias.Columns) != 3 || alias.ColumnDefinitionType.Name != "text" {
				t.Fatal("lost definition list", alias)
			}
			for i, name := range []string{"k", "v", "n"} {
				offset := strings.Index(input, name+" ")
				if alias.Columns[i].Text != name || alias.Columns[i].Pos.Offset != offset || alias.Columns[i].Pos.Character != utf8.RuneCountInString(input[:offset])+1 {
					t.Fatal("lost typed-member position", input, alias.Columns[i])
				}
			}
		}
	}
	for _, input := range []string{
		"SELECT * FROM json_each('{}') AS j(k text,)",
		"SELECT * FROM json_each('{}') AS j(k text,v)",
		"SELECT * FROM t AS j(k text,v json)",
	} {
		if statements, err := Parse(input); err == nil || statements != nil {
			t.Fatal("malformed/nonfunction list admitted", input, statements, err)
		}
	}
}
