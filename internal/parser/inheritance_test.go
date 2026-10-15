package parser

import (
	"strings"
	"testing"
	"unicode/utf8"
)

func TestInheritanceParentsAST(t *testing.T) {
	for _, head := range []string{"CREATE TABLE", "CREATE TABLE IF NOT EXISTS", "CREATE TEMP TABLE", "CREATE TEMP TABLE IF NOT EXISTS"} {
		for _, elements := range []string{"", "a INT NOT NULL"} {
			q := head + ` "échild"(` + elements + `) INHERITS("s"."p",other,"q"."r")`
			if strings.Contains(head, "TEMP") {
				q += " ON COMMIT DROP"
			}
			statements, err := Parse(q)
			if err != nil {
				t.Fatal(q, err)
			}
			table := statements[0].(*CreateTable)
			if table.Name.Parts[0].Text != "échild" || table.IfNotExists != strings.Contains(head, "IF NOT EXISTS") || len(table.Parents) != 3 {
				t.Fatal(q, table)
			}
			for i, parts := range [][]string{{"s", "p"}, {"other"}, {"q", "r"}} {
				parent := table.Parents[i]
				if len(parent.Parts) != len(parts) {
					t.Fatal(parent)
				}
				for j, name := range parts {
					identifier := parent.Parts[j]
					token := `"` + name + `"`
					if name == "other" {
						token = name
					}
					offset := strings.Index(q, token)
					if identifier.Text != name || identifier.Pos.Offset != offset || identifier.Pos.Character != utf8.RuneCountInString(q[:offset])+1 {
						t.Fatal(q, identifier)
					}
				}
			}
			if strings.Contains(head, "TEMP") && (table.Persistence != "temporary" || table.OnCommit != OnCommitDrop || table.OnCommitPos.Character != utf8.RuneCountInString(q[:strings.Index(q, "ON COMMIT")])+1) {
				t.Fatal(table)
			}
		}
	}
	statements, err := Parse("CREATE TABLE child(a INT)")
	if err != nil || statements[0].(*CreateTable).Parents != nil {
		t.Fatal(statements, err)
	}
}

func TestInheritanceInlineSchemaAST(t *testing.T) {
	statements, err := Parse("CREATE SCHEMA s CREATE TABLE parent(a INT) CREATE TABLE child() INHERITS(s.parent) CREATE TABLE IF NOT EXISTS other() INHERITS(parent,child)")
	if err != nil {
		t.Fatal(err)
	}
	schema := statements[0].(*CreateSchema)
	if len(schema.Tables) != 3 || schema.Tables[0].Parents != nil || len(schema.Tables[1].Parents) != 1 || len(schema.Tables[1].Parents[0].Parts) != 2 || schema.Tables[1].Parents[0].Parts[0].Text != "s" || schema.Tables[1].Parents[0].Parts[1].Text != "parent" || !schema.Tables[2].IfNotExists || len(schema.Tables[2].Parents) != 2 || schema.Tables[2].Parents[1].Parts[0].Text != "child" {
		t.Fatal(schema)
	}
}

func TestInheritanceMutationTargetsAST(t *testing.T) {
	for _, target := range []struct {
		text string
		only bool
	}{
		{`"é"."p"`, false}, {`"é"."p" *`, false},
		{`ONLY "é"."p"`, true}, {`ONLY("é"."p")`, true},
	} {
		for _, update := range []bool{false, true} {
			query := "DELETE FROM " + target.text + " AS named WHERE a=1 RETURNING a"
			if update {
				query = "UPDATE " + target.text + " AS named SET a=1 RETURNING a"
			}
			statements, err := Parse(query)
			if err != nil {
				t.Fatal(query, err)
			}
			var name ObjectName
			var alias Identifier
			var only, star bool
			if update {
				raw := statements[0].(*Update)
				name, alias, only, star = raw.Name, raw.Alias, raw.Only, raw.Star
			} else {
				raw := statements[0].(*Delete)
				name, alias, only, star = raw.Name, raw.Alias, raw.Only, raw.Star
			}
			if only != target.only || star != strings.HasSuffix(target.text, "*") || alias.Text != "named" || len(name.Parts) != 2 || name.Parts[0].Text != "é" || name.Parts[1].Text != "p" {
				t.Fatal(query, name, alias, only, star)
			}
			for _, identifier := range name.Parts {
				offset := strings.Index(query, `"`+identifier.Text+`"`)
				if identifier.Pos.Offset != offset || identifier.Pos.Character != utf8.RuneCountInString(query[:offset])+1 {
					t.Fatal(query, identifier)
				}
			}
		}
	}
}
