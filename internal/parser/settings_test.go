package parser

import (
	"context"
	"errors"
	"stuntgres/internal/sqlstate"
	"testing"
)

func TestSettingsGrammar(t *testing.T) {
	for _, q := range []string{"RESET application_name", "RESET ALL", "SHOW ALL", "SET demo.deep.value TO '3.5'", "SHOW demo.deep.value", "SELECT current_setting($1)", "SELECT current_setting(NULL)", "SELECT set_config($1,$2,$3)"} {
		s, err := Parse(q)
		if err != nil || len(s) != 1 {
			t.Errorf("%s: %v %v", q, s, err)
		}
	}
	for _, q := range []string{"SELECT current_setting()", "SELECT current_setting('a',true)", "SELECT set_config('a','b')", "SELECT current_setting(true)"} {
		_, err := Parse(q)
		var d *sqlstate.Error
		if !errors.As(err, &d) || d.Code != "0A000" || d.Message != "unsupported setting function signature" || d.Position != "8" {
			t.Errorf("%s: %v", q, err)
		}
	}
}
func TestSettingsOnKeyword(t *testing.T) {
	for _, q := range []string{"SET standard_conforming_strings TO on", "SET standard_conforming_strings = ON", "SET SESSION standard_conforming_strings TO on"} {
		statements, err := Parse(q)
		if err != nil || len(statements) != 1 {
			t.Fatalf("%s: %v %v", q, statements, err)
		}
		setting, ok := statements[0].(*Set)
		if !ok || len(setting.Values) != 1 || setting.Values[0] != "on" {
			t.Fatalf("%s: %#v", q, statements[0])
		}
	}
	// ON is still reserved and not a bare label outside its SET value role.
	for _, q := range []string{"CREATE TABLE on(v TEXT)", "SELECT v on FROM t", "SET on TO 'x'"} {
		_, err := Parse(q)
		var d *sqlstate.Error
		if !errors.As(err, &d) || d.Code != "42601" {
			t.Fatalf("%s: %v", q, err)
		}
	}
}

func TestSettingsRawScannerDefaults(t *testing.T) {
	for _, tc := range []struct{ query, want string }{
		{`'a\nb'`, `a\nb`},
		{`U&'\0061'`, "a"},
	} {
		l := &lexer{input: tc.query}
		var value sqlSymType
		if code := l.Lex(&value); code != STRING || value.tok.text != tc.want || l.err != nil {
			t.Fatalf("%s: %#v %v", tc.query, value.tok, l.err)
		}
	}
	// ParseWithOptions translates an explicitly selected off mode to the same
	// scanner flag; raw lexer construction alone must never imply off.
	l := &lexer{input: `U&'unterminated`, backslashEscapes: true}
	var value sqlSymType
	l.Lex(&value)
	var d *sqlstate.Error
	if !errors.As(l.err, &d) || d.Code != "0A000" || d.Position != "1" {
		t.Fatal(l.err)
	}
}
func TestSettingParserOptions(t *testing.T) {
	for _, standard := range []bool{true, false} {
		s, err := ParseWithOptions(`INSERT INTO t VALUES('a\nb',E'a\nb')`, Options{StandardConformingStrings: standard})
		if err != nil {
			t.Fatal(err)
		}
		row := s[0].(*Insert).Rows[0]
		want := "a\nb"
		if standard {
			want = `a\nb`
		}
		if row[0].(LiteralExpr).Value.Text != want || row[1].(LiteralExpr).Value.Text != "a\nb" {
			t.Fatal(standard, row)
		}
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := ParseWithOptions("SHOW ALL", Options{Context: ctx}); !errors.Is(err, context.Canceled) {
		t.Fatal(err)
	}
}

func TestSettingsKeywordAndFunctionComposition(t *testing.T) {
	for _, q := range []string{`CREATE TABLE local(local TEXT)`, `CREATE TABLE reset(reset TEXT)`, `CREATE TABLE current_setting(set_config TEXT)`, `SELECT set_config FROM local`, `SELECT "current_setting"('application_name')`, `SELECT CURRENT_SETTING('application_name')`} {
		if _, err := Parse(q); err != nil {
			t.Errorf("%s: %v", q, err)
		}
	}
	for _, q := range []string{`SELECT f(x) FROM t`, `INSERT INTO t VALUES(f('x'))`, `SELECT x FROM t WHERE x=f('x')`, `SELECT current_setting(current_setting('application_name'))`, `SELECT "CURRENT_SETTING"('application_name')`} {
		_, err := Parse(q)
		var d *sqlstate.Error
		if !errors.As(err, &d) || d.Code != "0A000" {
			t.Errorf("%s: %v", q, err)
		}
	}
	_, err := Parse(`CREATE TABLE all(v TEXT)`)
	var d *sqlstate.Error
	if !errors.As(err, &d) || d.Code != "42601" || d.Message != `syntax error at or near "all"` || d.Position != "14" {
		t.Fatal(err)
	}
	_, err = Parse(`SELECT set_config('application_name','a',)`)
	if !errors.As(err, &d) || d.Code != "42601" || d.Message != `syntax error at or near ")"` || d.Position != "42" {
		t.Fatal(err)
	}
}

func TestSettingsUnicodeOffOpenerPrecedence(t *testing.T) {
	for _, tc := range []struct{ query, position string }{
		{`SET application_name TO U&'\0061'`, "25"},
		{`SELECT current_setting(U&'application_name')`, "24"},
		{`SET application_name TO U&'\ZZZZ' UESCAPE '0'`, "25"},
		{`SET application_name TO U&'unterminated`, "25"},
		{`/*é*/ SET application_name TO U&'\ZZZZ'`, "31"},
	} {
		statements, err := ParseWithOptions(tc.query, Options{StandardConformingStrings: false})
		var got *sqlstate.Error
		want := sqlstate.Error{Severity: "ERROR", SeverityUnlocalized: "ERROR", Code: "0A000", Message: "unsafe use of string constant with Unicode escapes", Detail: `String constants with Unicode escapes cannot be used when "standard_conforming_strings" is off.`, Position: tc.position}
		if len(statements) != 0 || !errors.As(err, &got) || *got != want {
			t.Fatalf("%s: %#v %v", tc.query, got, statements)
		}
	}
	for _, q := range []string{`SET U&"application_name" TO 'x'`, `SET application_name TO E'a\nb'`, `SET application_name TO $$a\nb$$`} {
		if _, err := ParseWithOptions(q, Options{StandardConformingStrings: false}); err != nil {
			t.Fatal(q, err)
		}
	}
}

func TestSettingsUnicodeOffEarlierLexicalError(t *testing.T) {
	_, err := ParseWithOptions("SELECT \x00 U&'unterminated", Options{StandardConformingStrings: false})
	var got *sqlstate.Error
	if !errors.As(err, &got) || got.Code != "42601" || got.Message != "NUL in SQL" || got.Position != "8" {
		t.Fatal("earlier lexical error lost precedence", err)
	}
}

func TestSettingsRoleGrammarComposition(t *testing.T) {
	for _, query := range []string{
		"SET local TO value", "SET SESSION local = value",
		"SET local.deep.value TO 'x'", "SET role.deep.value TO 'x'",
		"RESET local.deep.value", "RESET role.deep.value", "RESET local",
		"SHOW local", "SHOW role", "SHOW ALL",
		"CREATE TABLE role(role TEXT)", "SELECT role FROM role",
		"SET application_name TO role",
		"CREATE ROLE current_setting LOGIN",
		"SET SESSION AUTHORIZATION current_setting",
		"RESET SESSION AUTHORIZATION", "SELECT current_user,session_user",
		"SELECT current_setting('application_name')",
	} {
		if statements, err := Parse(query); err != nil || len(statements) != 1 {
			t.Fatalf("%s: %v %v", query, statements, err)
		}
	}
	for _, tc := range []struct{ query, message, position string }{
		{"SET LOCAL ROLE reader", "SET ROLE is unsupported", "11"},
		{"SET LOCAL ROLE TO reader", "SET ROLE is unsupported", "11"},
		{"SET LOCAL ROLE = 'reader'", "SET ROLE is unsupported", "11"},
		{"SET ROLE reader", "SET ROLE is unsupported", "5"},
		{"RESET ROLE", "RESET ROLE is unsupported", "7"},
	} {
		_, err := Parse(tc.query)
		var diagnostic *sqlstate.Error
		if !errors.As(err, &diagnostic) || diagnostic.Code != "0A000" || diagnostic.Message != tc.message || diagnostic.Position != tc.position {
			t.Fatalf("%s: %v", tc.query, err)
		}
	}
}
