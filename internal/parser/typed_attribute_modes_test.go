package parser

import "testing"

func TestTypedAttributeDependencyModeTerminators(t *testing.T) {
	for _, declaration := range []string{"smallint", "int2", "double precision", "character varying(5)", "public.member_type", "cascade"} {
		for _, mode := range []struct {
			word string
			want DropMode
		}{{"CASCADE", DropCascade}, {"RESTRICT", DropRestrict}, {"", DropRestrict}} {
			for _, operation := range []string{"ADD ATTRIBUTE flag ", "ALTER ATTRIBUTE flag TYPE "} {
				query := "ALTER TYPE pair " + operation + declaration + " " + mode.word
				statements, err := Parse(query)
				if err != nil {
					t.Fatal(query, err)
				}
				statement, ok := statements[0].(*AlterComposite)
				if !ok || len(statement.Actions) != 1 || statement.Actions[0].Mode != mode.want {
					t.Fatal(query, statements)
				}
				action := statement.Actions[0]
				name := action.TypeName.Name
				if action.Definition != nil {
					name = action.Definition.TypeName.Name
				}
				expected := declaration
				if declaration == "int2" {
					expected = "smallint"
				} // Existing parser canonical alias.
				if declaration == "character varying(5)" {
					expected = "character varying"
				}
				if declaration == "public.member_type" {
					expected = "member_type"
				}
				if name != expected {
					t.Fatal(query, "mode consumed by type", name)
				}
			}
		}
	}
	statements, err := Parse("ALTER TYPE pair ADD ATTRIBUTE a smallint CASCADE, ALTER ATTRIBUTE flag TYPE smallint RESTRICT")
	if err != nil {
		t.Fatal(err)
	}
	statement := statements[0].(*AlterComposite)
	if len(statement.Actions) != 2 || statement.Actions[0].Mode != DropCascade || statement.Actions[1].Mode != DropRestrict {
		t.Fatal(statement)
	}
}
