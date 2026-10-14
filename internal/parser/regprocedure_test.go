package parser

import (
	"errors"
	"reflect"
	"strings"
	"stuntgres/internal/sqlstate"
	"testing"
)

func TestRegprocedureDecodedSignature(t *testing.T) {
	for _, test := range []struct {
		input       string
		names, args []string
	}{
		{`  PG_CATALOG.ABS ( numeric(8,2), text[2][3] ) `, []string{"pg_catalog", "abs"}, []string{"numeric(8,2)", "text[2][3]"}},
		{`"s(p"."F""oo"("a,b", "q""uote"[])`, []string{"s(p", `F"oo`}, []string{`"a,b"`, `"q""uote"[]`}},
		{"now ( \t )", []string{"now"}, nil},
		{strings.Repeat("é", 32) + "()", []string{strings.Repeat("é", 31)}, nil},
	} {
		var args []string
		name, err := ParseRegprocedureName(test.input, func(argument string) error { args = append(args, argument); return nil })
		if err != nil {
			t.Fatal(test.input, err)
		}
		var names []string
		for _, part := range name.Parts {
			names = append(names, part.Text)
		}
		if !reflect.DeepEqual(names, test.names) || !reflect.DeepEqual(args, test.args) {
			t.Fatal(test.input, names, args)
		}
	}
	count := 0
	name, err := ParseRegprocedureName("now("+strings.Repeat("integer,", 99)+"integer)", func(string) error { count++; return nil })
	if err != nil || count != 100 || name.Parts[0].Text != "now" {
		t.Fatal("100 arguments", count, err)
	}
}

func TestRegprocedureSignatureDiagnostics(t *testing.T) {
	for _, test := range []struct{ input, code, message string }{
		{"now", "22P02", "expected a left parenthesis"},
		{`"now(`, "22P02", "expected a left parenthesis"},
		{"now(", "22P02", "expected a right parenthesis"},
		{"now() suffix", "22P02", "expected a right parenthesis"},
		{"a..b(integer)", "42602", "invalid name syntax"},
		{"abs(integer,)", "22P02", "expected a type name"},
		{"abs(,integer)", "22P02", "expected a type name"},
		{"abs(integer[)", "22P02", "improper type name"},
		{"abs(numeric(8,2)", "22P02", "improper type name"},
		{`abs("unclosed)`, "22P02", "improper type name"},
		{"now(" + strings.Repeat("integer,", 100) + "integer)", "54023", "too many arguments"},
	} {
		_, err := ParseRegprocedureName(test.input, func(string) error { return nil })
		var diagnostic *sqlstate.Error
		if !errors.As(err, &diagnostic) || diagnostic.Code != test.code || diagnostic.Message != test.message || diagnostic.Position != "" || diagnostic.Context != "" {
			t.Fatalf("%q: %#v", test.input, err)
		}
	}
}

func TestRegprocedureArgumentErrorPrecedence(t *testing.T) {
	sentinel := errors.New("argument type resolution failed")
	for _, input := range []string{"abs(missing,integer[)", "now(missing," + strings.Repeat("integer,", 100) + "integer)"} {
		count := 0
		_, err := ParseRegprocedureName(input, func(string) error { count++; return sentinel })
		if err != sentinel || count != 1 {
			t.Fatal("earlier type error lost", count, err)
		}
	}
}
