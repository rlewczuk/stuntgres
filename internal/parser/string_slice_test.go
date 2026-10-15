package parser

import (
	"errors"
	"stuntgres/internal/sqlstate"
	"testing"
)

func TestStringSliceParserAdmission(t *testing.T) {
	for _, tc := range []struct {
		query, name string
		arity       int
		syntax      FunctionSyntax
	}{
		{"SELECT left('abc',1)", "left", 2, OrdinaryFunctionSyntax},
		{"SELECT right('abc',1)", "right", 2, OrdinaryFunctionSyntax},
		{`SELECT "LEFT"('abc',1)`, "LEFT", 2, OrdinaryFunctionSyntax},
		{"SELECT pg_catalog.left('abc',1)", "left", 2, OrdinaryFunctionSyntax},
		{"SELECT pg_catalog.right('abc',1)", "right", 2, OrdinaryFunctionSyntax},
		{`SELECT pg_catalog."left"('abc',1)`, "left", 2, OrdinaryFunctionSyntax},
		{`SELECT pg_catalog."right"('abc',1)`, "right", 2, OrdinaryFunctionSyntax},
		{"SELECT substring('abc' FROM 2 FOR 1)", "substring", 3, SQLFunctionSyntax},
		{"SELECT substring('abc' FOR 1 FROM 2)", "substring", 3, SQLFunctionSyntax},
		{"SELECT substring('abc' FROM 2)", "substring", 2, SQLFunctionSyntax},
		{"SELECT substring('abc' FOR '2')", "substring", 3, SQLFunctionSyntax},
		{"SELECT substr('abc',2)", "substr", 2, OrdinaryFunctionSyntax},
		{"SELECT right('abc',VARIADIC 1)", "right", 2, OrdinaryFunctionSyntax},
	} {
		t.Run(tc.query, func(t *testing.T) {
			statements, err := Parse(tc.query)
			if err != nil {
				t.Fatal(err)
			}
			selected, ok := statements[0].(*Select)
			if !ok {
				t.Fatal(statements)
			}
			call, ok := selected.Targets[0].(FunctionExpr)
			if !ok || call.Name.Parts[len(call.Name.Parts)-1].Text != tc.name || len(call.Args) != tc.arity || call.Syntax != tc.syntax || call.Pos.Character != 8 {
				t.Fatal("lost call provenance", call)
			}
			if tc.query == "SELECT substring('abc' FOR '2')" {
				start, ok := call.Args[1].(LiteralExpr)
				if !ok || start.Value.Integer != 1 {
					t.Fatal("FOR-only lost synthesized start", call)
				}
				count, ok := call.Args[2].(CastExpr)
				if !ok || count.TypeName.Name != "int4" {
					t.Fatal("FOR-only lost integer count cast", call)
				}
			}
		})
	}
	for _, query := range []string{
		"SELECT a.id FROM a LEFT JOIN b ON a.id=b.id",
		"SELECT a.id FROM a RIGHT JOIN b ON a.id=b.id",
		"SELECT left(a.s,1) FROM a LEFT JOIN b ON right(a.s,1)=b.s",
	} {
		if _, err := Parse(query); err != nil {
			t.Fatal("slicing admission broke JOIN", query, err)
		}
	}
}

func TestStringSliceParserQualifiedNames(t *testing.T) {
	for _, name := range []string{"left", "right"} {
		for _, query := range []string{
			"CREATE TABLE public." + name + " (x INT)",
			"INSERT INTO public." + name + " (x) VALUES (7)",
			"SELECT x FROM public." + name + " WHERE x=7 ORDER BY x",
			"CREATE TABLE public.\"" + name + "\" (x INT)",
			"INSERT INTO public.\"" + name + "\" (x) VALUES (7)",
		} {
			statements, err := Parse(query)
			if err != nil || len(statements) != 1 {
				t.Fatalf("slicing call admission changed qualified relation %q: %v", query, err)
			}
		}
	}
}

func TestStringSliceParserPositions(t *testing.T) {
	for _, tc := range []struct{ query, message, position string }{
		{"SELECT substring('abc' FROM)", `syntax error at or near ")"`, "28"},
		{"SELECT left('abc',1) trailing junk", `syntax error at or near "junk"`, "31"},
		{"SELECT right('abc',)", `syntax error at or near ")"`, "20"},
	} {
		_, err := Parse(tc.query)
		var diagnostic *sqlstate.Error
		if !errors.As(err, &diagnostic) || diagnostic.Code != "42601" || diagnostic.Message != tc.message || diagnostic.Position != tc.position {
			t.Fatal(tc, err)
		}
	}
}
