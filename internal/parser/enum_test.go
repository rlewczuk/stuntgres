package parser

import (
	"reflect"
	"strings"
	"testing"
)

func TestEnumDeclarationAST(t *testing.T) {
	query := `/*é*/ CREATE TYPE public."Odd Type" AS ENUM ('sad','','O''K','back\slash')`
	statements, err := Parse(query)
	if err != nil || len(statements) != 1 {
		t.Fatal(statements, err)
	}
	declaration, ok := statements[0].(*CreateEnum)
	if !ok {
		t.Fatalf("%T", statements[0])
	}
	if len(declaration.Name.Parts) != 2 || declaration.Name.Parts[0].Text != "public" || declaration.Name.Parts[1].Text != "Odd Type" || declaration.Pos.Character != 7 {
		t.Fatal(declaration)
	}
	labels := make([]string, len(declaration.Labels))
	for i, label := range declaration.Labels {
		labels[i] = label.Text
		if label.Pos.Offset < 0 || query[label.Pos.Offset] != '\'' {
			t.Fatal("label position", label)
		}
	}
	if !reflect.DeepEqual(labels, []string{"sad", "", "O'K", `back\slash`}) {
		t.Fatal(labels)
	}
	empty, err := Parse("CREATE TYPE empty_enum AS ENUM ()")
	if err != nil || len(empty[0].(*CreateEnum).Labels) != 0 {
		t.Fatal(empty, err)
	}
	for _, query := range []string{"CREATE TYPE mood AS ENUM ('sad',)", "CREATE TYPE mood AS ENUM ('sad') nonsense", "CREATE TYPE mood AS ENUM (sad)"} {
		if _, err := Parse(query); err == nil {
			t.Fatal("invalid enum syntax accepted", query)
		}
	}
	// Unreserved keyword identity is required; quoted lookalikes are names.
	for _, query := range []string{`CREATE "type" mood AS ENUM ('sad')`, `CREATE TYPE mood AS "enum" ('sad')`} {
		if _, err := Parse(query); err == nil {
			t.Fatal("quoted keyword accepted", query)
		}
	}
	if declaration.Labels[0].Pos.Offset != strings.Index(query, "'sad'") {
		t.Fatal("original source location lost")
	}
}

func TestEnumAndDomainDeclarationHeads(t *testing.T) {
	statements, err := Parse(`CREATE TYPE enum_saved.mood AS ENUM ('sad','ok');
CREATE DOMAIN enum_saved.wrapped AS enum_saved.mood;
CREATE DOMAIN type enum_saved.wrapped`)
	if err != nil || len(statements) != 3 {
		t.Fatal(statements, err)
	}
	enum, ok := statements[0].(*CreateEnum)
	if !ok || enum.Name.Parts[1].Text != "mood" {
		t.Fatal("enum declaration", statements[0])
	}
	for i, want := range []struct {
		name string
		base string
	}{
		{"wrapped", "mood"},
		{"type", "wrapped"},
	} {
		domain, ok := statements[i+1].(*CreateDomain)
		if !ok || domain.Name.Parts[len(domain.Name.Parts)-1].Text != want.name ||
			domain.Base.Name != want.base || domain.Base.Schema == nil ||
			domain.Base.Schema.Text != "enum_saved" {
			t.Fatal("domain declaration", statements[i+1])
		}
	}
	for _, query := range []string{
		"CREATE TYPE type AS ENUM ('ok')",
		"CREATE TYPE enum AS ENUM ('ok')",
		"CREATE DOMAIN domain AS type",
		"CREATE TABLE type(type type, enum enum, domain domain)",
		"SELECT type, enum, domain FROM type",
		"SELECT type AS type, enum enum FROM domain",
		"SET application_name TO type",
		"ALTER TABLE type ALTER COLUMN type TYPE type",
		"ALTER TABLE type ALTER type SET DATA TYPE enum",
	} {
		if _, err := Parse(query); err != nil {
			t.Fatal("unreserved type/enum/domain name rejected", query, err)
		}
	}
}

func TestEnumSingletonSyntaxCandidate(t *testing.T) {
	for _, query := range []string{
		"SELECT 'ok'::mood",
		"SELECT $1::mood",
		"SELECT NULL::replacement",
		"SELECT 'ok'::mood(1)",
		"SELECT 'ok'::mood COLLATE \"C\"",
		"SELECT 'ok'::mood = 'ok'::happiness",
		"SELECT 'ok'::mood::text = 'ok'::happiness::text",
		"SELECT NULL::mood IS NULL",
		"SELECT 'ok'::mood, $1::mood",
		// Syntax has no catalog: these candidates must fail the engine gate.
		"SELECT 1::integer",
		"SELECT 'ok'::wrapped",
	} {
		statements, err := Parse(query)
		if err != nil || len(statements) != 1 {
			t.Fatal(query, statements, err)
		}
		selected := statements[0].(*Select)
		if !selected.IsTypeSingleton() || selected.IsSingleton() {
			t.Fatal("cast candidate changed existing singleton authority", query)
		}
	}
	for _, query := range []string{
		"SELECT 1",
		"SELECT 'ok'::mood, 1",
		"SELECT 'ok'::mood + 1",
		"SELECT m::mood",
		"SELECT count(*)",
		"SELECT 'ok'::mood ORDER BY 1",
	} {
		statements, err := Parse(query)
		if err == nil && len(statements) == 1 && statements[0].(*Select).IsTypeSingleton() {
			t.Fatal("unrelated singleton admitted", query)
		}
	}
}
