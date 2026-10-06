package parser

import (
	"strings"
	"testing"
	"unicode/utf8"
)

func TestCharacterDeclarationsAndScalarSyntax(t *testing.T) {
	for _, decl := range []string{"char", "character", "char(3)", "character(3)", "bpchar(3)", "bpchar", "varchar", "character varying", "varchar(3)", "character varying(3)", `"bpchar"(3)`, `"varchar"(3)`} {
		t.Run(decl, func(t *testing.T) {
			stmts, err := Parse("/*é*/ CREATE TABLE c(v " + decl + ")")
			if err != nil {
				t.Fatal(err)
			}
			raw := stmts[0].(*CreateTable).Elements[0].Column.TypeName
			if raw.Pos.Character == 0 {
				t.Fatal("lost type position", raw)
			}
			if len(raw.Modifiers) > 0 && (raw.Modifiers[0].Integer == nil || *raw.Modifiers[0].Integer != 3) {
				t.Fatal("lost length", raw)
			}
		})
	}
	for _, q := range []string{"SELECT CAST('too long' AS varchar(5)) FROM seed", "SELECT 'too long'::char(5) FROM seed", "SELECT CAST(CAST($1 AS char(3)) AS text) FROM seed", "SELECT CAST(c AS varchar(2)) FROM c", "SELECT c FROM c WHERE c=d", "SELECT c FROM c WHERE CAST('a ' AS text)=c", "SELECT c FROM c WHERE 'a    '=c", "SELECT CAST(c AS text)='a',c='a   ' FROM c", "SELECT c FROM c WHERE CAST(c AS text)='a' AND NOT CAST('a ' AS text)=c"} {
		t.Run(q, func(t *testing.T) {
			if _, err := Parse(q); err != nil {
				t.Fatal(err)
			}
		})
	}
}

func TestCharacterNestedCastASTPositions(t *testing.T) {
	query := "/*é🙂*/ SELECT CAST(CAST($1 AS char(3)) AS varchar(2)) FROM seed"
	statements, err := Parse(query)
	if err != nil {
		t.Fatal(err)
	}
	outer, ok := statements[0].(*Select).Targets[0].(CastExpr)
	if !ok {
		t.Fatal("missing outer cast")
	}
	inner, ok := outer.Child.(CastExpr)
	if !ok {
		t.Fatal("missing inner cast")
	}
	parameter, ok := inner.Child.(ParameterExpr)
	if !ok || parameter.Number != 1 {
		t.Fatal(inner.Child)
	}
	for _, expected := range []struct {
		pos   Position
		token string
	}{
		{outer.Pos, "CAST(CAST"}, {outer.TypeName.Pos, "varchar"},
		{inner.Pos, "CAST($1"}, {inner.TypeName.Pos, "char"},
		{parameter.Pos, "$1"},
	} {
		offset := strings.Index(query, expected.token)
		character := utf8.RuneCountInString(query[:offset]) + 1
		if expected.pos != (Position{Offset: offset, Character: character}) {
			t.Fatal(expected, offset, character)
		}
	}
	if outer.TypeName.Name != "varchar" || len(outer.TypeName.Modifiers) != 1 || outer.TypeName.Modifiers[0].Integer == nil || *outer.TypeName.Modifiers[0].Integer != 2 {
		t.Fatal(outer.TypeName)
	}
	if inner.TypeName.Name != "char" || len(inner.TypeName.Modifiers) != 1 || inner.TypeName.Modifiers[0].Integer == nil || *inner.TypeName.Modifiers[0].Integer != 3 {
		t.Fatal(inner.TypeName)
	}
}

func TestCharacterGenericIdentifierModifiers(t *testing.T) {
	for _, base := range []string{"bpchar", `"bpchar"`, `"varchar"`, "text", `"text"`} {
		for _, identifier := range []struct{ source, decoded string }{{"foo", "foo"}, {`"2"`, "2"}, {`"Foo"`, "Foo"}} {
			q := "/*é🙂*/ CREATE TABLE c(v " + base + "(" + identifier.source + "))"
			statements, err := Parse(q)
			if err != nil {
				t.Fatal(q, err)
			}
			modifiers := statements[0].(*CreateTable).Elements[0].Column.TypeName.Modifiers
			if len(modifiers) != 1 || modifiers[0].Integer != nil || modifiers[0].Raw != identifier.decoded {
				t.Fatal("identifier modifier provenance", q, modifiers)
			}
			offset := strings.Index(q, identifier.source)
			want := Position{Offset: offset, Character: utf8.RuneCountInString(q[:offset]) + 1}
			if modifiers[0].Pos != want {
				t.Fatal(q, modifiers[0].Pos, want)
			}
		}
	}
	for _, decl := range []string{"char(foo)", "varchar(foo)", "numeric(foo)", `"timestamp"(foo)`, "bpchar(foo.bar)", "bpchar(foo+1)", "bpchar(foo())"} {
		if _, err := Parse("CREATE TABLE c(v " + decl + ")"); err == nil {
			t.Fatal("widened modifier syntax", decl)
		}
	}
}
