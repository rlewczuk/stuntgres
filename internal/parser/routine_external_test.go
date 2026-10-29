package parser

import (
	"strings"
	"testing"
	"unicode/utf8"
)

func TestRoutineExternalASItems(t *testing.T) {
	for _, tc := range []struct {
		q         string
		body      string
		symbol    string
		second    bool
		procedure bool
		delimiter string
	}{
		{"/*é*/ CREATE FUNCTION f(integer) RETURNS integer LANGUAGE c AS $obj$lib'file$obj$, ''", "lib'file", "", true, false, "$obj$"},
		{"CREATE FUNCTION f(integer) RETURNS integer AS 'lib''file', $sym$symbol$sym$ LANGUAGE c", "lib'file", "symbol", true, false, "'"},
		{"CREATE PROCEDURE f() LANGUAGE c AS 'library', 'symbol'", "library", "symbol", true, true, "'"},
		{"CREATE PROCEDURE f() AS $$library$$ LANGUAGE c", "library", "", false, true, "$$"},
	} {
		statements, err := Parse(tc.q)
		if err != nil || len(statements) != 1 {
			t.Fatal(tc.q, statements, err)
		}
		s, ok := statements[0].(*CreateFunction)
		if !ok {
			t.Fatalf("routine AST: %T", statements[0])
		}
		if s.Body != tc.body || s.Symbol != tc.symbol || s.SymbolSpecified != tc.second || s.Procedure != tc.procedure {
			t.Fatalf("AS item distinction: %#v", s)
		}
		origin := strings.Index(tc.q, tc.delimiter) + len(tc.delimiter) - 1
		if s.BodyPos.Offset != origin || s.BodyPos.Character != utf8.RuneCountInString(tc.q[:origin])+1 {
			t.Fatalf("body content origin: %#v, byte %d", s.BodyPos, origin)
		}
		if tc.second {
			comma := strings.Index(tc.q, ",")
			if s.SymbolPos.Offset <= comma || s.SymbolPos.Character <= 0 {
				t.Fatal("second AS origin", s.SymbolPos)
			}
		} else if s.SymbolPos != (Position{}) {
			t.Fatal("omitted symbol has an origin", s.SymbolPos)
		}
	}
}

func TestRoutineExternalHandlerSyntax(t *testing.T) {
	q := `/*é*/ CREATE OR REPLACE TRUSTED PROCEDURAL LANGUAGE "Mixed Language" HANDLER "Handler Schema"."Call Handler" INLINE pg_catalog.plpgsql_inline_handler VALIDATOR pg_catalog.plpgsql_validator`
	statements, err := Parse(q)
	if err != nil || len(statements) != 1 {
		t.Fatal(statements, err)
	}
	s, ok := statements[0].(*CreateLanguage)
	if !ok {
		t.Fatalf("language AST: %T", statements[0])
	}
	if !s.Replace || !s.Trusted || !s.Procedural || s.Name.Text != "Mixed Language" || s.Pos.Offset != len("/*é*/ ") {
		t.Fatalf("language flags/name: %#v", s)
	}
	if len(s.Handler.Parts) != 2 || s.Handler.Parts[0].Text != "Handler Schema" || s.Handler.Parts[1].Text != "Call Handler" || len(s.Inline.Parts) != 2 || len(s.Validator.Parts) != 2 {
		t.Fatalf("handler names: %#v", s)
	}
	for _, n := range []Identifier{s.Name, s.Handler.Parts[0], s.Handler.Parts[1], s.Inline.Parts[0], s.Validator.Parts[0]} {
		if n.Pos.Offset <= s.Pos.Offset || n.Pos.Character != utf8.RuneCountInString(q[:n.Pos.Offset])+1 {
			t.Fatal("identifier origin", n)
		}
	}
	for _, source := range []string{
		"CREATE LANGUAGE l HANDLER h",
		"CREATE PROCEDURAL LANGUAGE l HANDLER h VALIDATOR v",
		"CREATE TRUSTED LANGUAGE l HANDLER h INLINE i",
	} {
		statements, err := Parse(source)
		if err != nil || len(statements) != 1 {
			t.Fatal(source, statements, err)
		}
	}
	// Command terminals retain PostgreSQL's unreserved identifier legality.
	statements, err = Parse("CREATE LANGUAGE trusted HANDLER procedural INLINE inline")
	if err != nil || len(statements) != 1 {
		t.Fatal(statements, err)
	}
	names := statements[0].(*CreateLanguage)
	if names.Name.Text != "trusted" || names.Handler.Parts[0].Text != "procedural" || names.Inline.Parts[0].Text != "inline" || names.Trusted || names.Procedural {
		t.Fatalf("unreserved command words: %#v", names)
	}
	for _, suffix := range []string{" trailing", " INLINE pg_catalog.other", ", 'extra'"} {
		if _, err := Parse(q + suffix); err == nil {
			t.Fatal("accepted trailing language input", suffix)
		}
	}
}

func TestRoutineLanguageLifecycleSyntax(t *testing.T) {
	for _, prefix := range []string{"", "PROCEDURAL "} {
		statements, err := Parse(`ALTER ` + prefix + `LANGUAGE "Old Name" RENAME TO "New Name"`)
		if err != nil || len(statements) != 1 {
			t.Fatal(statements, err)
		}
		rename, ok := statements[0].(*AlterLanguage)
		if !ok || rename.Name.Text != "Old Name" || rename.Target.Text != "New Name" || rename.Action != "rename" {
			t.Fatal("global rename lost decoded identity", statements)
		}
		statements, err = Parse("ALTER " + prefix + "LANGUAGE l OWNER TO CURRENT_USER")
		if err != nil || len(statements) != 1 {
			t.Fatal(statements, err)
		}
		owner := statements[0].(*AlterLanguage)
		if owner.Action != "owner" || !owner.OwnerIdentity || owner.Target.Text != "current_user" {
			t.Fatal("owner identity became a literal role name", owner)
		}
		statements, err = Parse(`DROP ` + prefix + `LANGUAGE IF EXISTS "Mixed Language",folded CASCADE`)
		if err != nil || len(statements) != 1 {
			t.Fatal(statements, err)
		}
		drop, ok := statements[0].(*DropLanguage)
		if !ok || !drop.IfExists || drop.Mode != DropCascade || len(drop.Names) != 2 || drop.Names[0].Text != "Mixed Language" || drop.Names[1].Text != "folded" {
			t.Fatal("drop lost complete global target set", statements)
		}
	}
	for _, source := range []string{"CREATE LANGUAGE unaccent", "CREATE OR REPLACE TRUSTED PROCEDURAL LANGUAGE unaccent"} {
		statements, err := Parse(source)
		if err != nil || len(statements) != 1 {
			t.Fatal(statements, err)
		}
		extension, ok := statements[0].(*CreateExtension)
		if !ok || extension.Name.Text != "unaccent" || extension.IfNotExists != strings.Contains(source, "REPLACE") {
			t.Fatal("legacy declaration failed extension normalization", statements)
		}
	}
	for _, source := range []string{"ALTER LANGUAGE l RENAME TO n trailing", "ALTER LANGUAGE l OWNER TO r trailing", "DROP LANGUAGE l CASCADE trailing", "CREATE LANGUAGE l HANDLER h VALIDATOR v INLINE i"} {
		if _, err := Parse(source); err == nil {
			t.Fatal("accepted trailing or reordered language clauses", source)
		}
	}
}
