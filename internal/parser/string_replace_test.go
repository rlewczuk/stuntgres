package parser

import (
	"errors"
	"testing"

	"stuntgres/internal/sqlstate"
)

func TestStringReplacementParserProvenance(t *testing.T) {
	for _, tc := range []struct {
		query   string
		arity   int
		special bool
	}{
		{"SELECT overlay('abc' PLACING 'X' FROM 2)", 3, true},
		{"SELECT overlay('abc' PLACING 'X' FROM 2 FOR 1)", 4, true},
		{"SELECT overlay('abc','X',2)", 3, false},
		{`SELECT "overlay"('abc','X',2,1)`, 4, false},
		{"SELECT pg_catalog.overlay('abc','X',2)", 3, false},
		{"SELECT replace('abc','b','X')", 3, false},
		{"SELECT REPLACE('abc','b','X')", 3, false},
		{"SELECT pg_catalog.replace('abc','b','X')", 3, false},
		{"SELECT translate('abc','b','X')", 3, false},
		{"SELECT starts_with('abc','a')", 2, false},
		{`SELECT "REPLACE"('abc','b','X')`, 3, false},
	} {
		t.Run(tc.query, func(t *testing.T) {
			statements, err := Parse(tc.query)
			if err != nil {
				t.Fatal(err)
			}
			selected, ok := statements[0].(*Select)
			if !ok || selected.From != nil {
				t.Fatal("selected admission", statements)
			}
			call, ok := selected.Targets[0].(FunctionExpr)
			if !ok || len(call.Args) != tc.arity || call.Pos.Character != 8 {
				t.Fatal(call)
			}
			if tc.special && (call.Syntax != SQLFunctionSyntax || call.StringSyntax != OverlayStringSyntax || len(call.Name.Parts) != 2 || call.Name.Parts[0].Text != "pg_catalog") {
				t.Fatal("lost overlay provenance", call)
			}
			if !tc.special && (call.Syntax != OrdinaryFunctionSyntax || call.StringSyntax != OrdinaryStringSyntax) {
				t.Fatal("ordinary syntax changed", call)
			}
		})
	}
	for _, query := range []string{"SELECT 'abc' ^@ 'a'", "SELECT 'abc' OPERATOR(pg_catalog.^@) 'a'"} {
		statements, err := Parse(query)
		if err != nil {
			t.Fatal(err)
		}
		selected := statements[0].(*Select)
		op, ok := selected.Targets[0].(OperatorExpr)
		if !ok || selected.From != nil || len(op.Args) != 2 || op.Name.Parts[len(op.Name.Parts)-1].Text != "^@" || op.Pos.Character != 14 {
			t.Fatal(query, op)
		}
	}
}

func TestStringReplacementParserVariadicBoundary(t *testing.T) {
	for _, tc := range []struct {
		query    string
		position string
	}{
		{"SELECT overlay('abc','X',VARIADIC 2)", "26"},
		{"SELECT overlay('abc','X',2,VARIADIC 1)", "28"},
	} {
		_, err := Parse(tc.query)
		var d *sqlstate.Error
		if !errors.As(err, &d) || d.Code != "42601" || d.Message != `syntax error at or near "VARIADIC"` || d.Position != tc.position {
			t.Fatal(tc, d, err)
		}
	}
	for _, query := range []string{"SELECT overlay()", "SELECT replace('abc','b',VARIADIC 'X')", "SELECT translate('abc','b',VARIADIC 'X')", "SELECT starts_with('abc',VARIADIC 'a')", "SELECT substr(v,VARIADIC 1) FROM t"} {
		if _, err := Parse(query); err != nil {
			t.Fatal("unrelated existing admission changed", query, err)
		}
	}
}

func TestStringReplacementKeywordBoundary(t *testing.T) {
	for _, query := range []string{
		"SELECT replace FROM t",
		"SELECT 1 AS replace FROM t",
		"CREATE TABLE replace(replace text)",
	} {
		if _, err := Parse(query); err != nil {
			t.Fatal("REPLACE name admission changed", query, err)
		}
	}
	statements, err := Parse("CREATE OR REPLACE VIEW sr_keyword AS SELECT replace('abc','b','X') AS replace")
	if err != nil {
		t.Fatal(err)
	}
	view, ok := statements[0].(*CreateView)
	if !ok || !view.Replace {
		t.Fatal("lost CREATE OR REPLACE VIEW", statements)
	}
}
