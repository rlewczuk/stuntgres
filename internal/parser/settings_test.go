package parser

import (
	"context"
	"errors"
	"stuntgres/internal/sqlstate"
	"testing"
)

func TestSettingsGrammar(t *testing.T) {
	for _, q := range []string{"RESET application_name", "RESET ALL", "SHOW ALL", "SET demo.deep.value TO '3.5'", "SHOW demo.deep.value", "SELECT current_setting($1)", "SELECT current_setting(NULL)", "SELECT current_setting('a',true)", "SELECT current_setting($1,$2)", "SELECT current_setting('a',NULL)", "SELECT set_config($1,$2,$3)"} {
		s, err := Parse(q)
		if err != nil || len(s) != 1 {
			t.Errorf("%s: %v %v", q, s, err)
		}
	}
	for _, q := range []string{"SELECT current_setting()", "SELECT current_setting('a',true,false)", "SELECT set_config('a','b')", "SELECT current_setting(true)"} {
		_, err := Parse(q)
		var d *sqlstate.Error
		if !errors.As(err, &d) || d.Code != "0A000" || d.Message != "unsupported setting function signature" || d.Position != "8" {
			t.Errorf("%s: %v", q, err)
		}
	}
	for _, tc := range []struct {
		query       string
		start, name int
	}{
		{"SELECT current_setting($1::TEXT)", 8, 8},
		{"SELECT pg_catalog.current_setting($1::TEXT)", 8, 19},
		{"/*é*/ SELECT pg_catalog.current_setting(a.k)", 14, 25},
	} {
		statements, err := Parse(tc.query)
		if err != nil || len(statements) != 1 {
			t.Fatal(tc.query, statements, err)
		}
		call, ok := statements[0].(*SettingCall)
		if !ok || call.Pos.Character != tc.start || call.NamePos.Character != tc.name {
			t.Fatal("setting call lost component positions", tc.query, call)
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
	for _, q := range []string{`CREATE TABLE local(local TEXT)`, `CREATE TABLE reset(reset TEXT)`, `CREATE TABLE current_setting(set_config TEXT)`, `SELECT set_config FROM local`, `SELECT "current_setting"('application_name')`, `SELECT CURRENT_SETTING('application_name')`, `SELECT current_setting(current_setting('application_name'))`, `SELECT current_setting(a.k)`, `SELECT current_setting(qa.a.k)`} {
		if _, err := Parse(q); err != nil {
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
}

func TestSettingsXMLOption(t *testing.T) {
	for _, scope := range []string{"", "SESSION ", "LOCAL "} {
		for _, option := range []string{"DOCUMENT", "CONTENT"} {
			query := "SET " + scope + "XML OPTION " + option
			statements, err := Parse(query)
			if err != nil || len(statements) != 1 {
				t.Fatal(query, statements, err)
			}
			s, ok := statements[0].(*Set)
			if !ok || s.Name.Text != "xmloption" || s.Local != (scope == "LOCAL ") || len(s.Values) != 1 || s.Values[0] != fold(option) {
				t.Fatalf("%s: %#v", query, statements[0])
			}
		}
	}
	for _, query := range []string{
		"CREATE TABLE xml(xml xml)",
		"SELECT xml(xml) FROM xml",
		"SELECT xml 'body'",
		"CREATE ROLE xml LOGIN",
		"SET xml='value'",
		"SET CONSTRAINTS ALL IMMEDIATE",
		"SET CONSTRAINTS xml DEFERRED",
		"DO $$DECLARE xml integer; BEGIN xml := 1; END$$",
	} {
		if _, err := Parse(query); err != nil {
			t.Fatal("XML keyword context", query, err)
		}
	}
	for _, query := range []string{"SET XML OPTION", "SET XML OPTION other", "SET XML OPTION DOCUMENT trailing", "SET XML OPTION DOCUMENT SELECT 1", `SET "xml" OPTION DOCUMENT`, `SET XML OPTION "document"`, "SET other OPTION DOCUMENT"} {
		if _, err := Parse(query); err == nil {
			t.Fatal("accepted invalid XML OPTION", query)
		}
	}
}

func TestSettingsClientListFlattening(t *testing.T) {
	for _, tc := range []struct{ query, first string }{
		{`SET createrole_self_grant TO 'SET','INHERIT'`, "SET"},
		{`SET restrict_nonsystem_relation_kind TO 'VIEW','foreign-table'`, "VIEW"},
		{`SET temp_tablespaces TO 'pg_default',''`, "pg_default"},
		{`SET local_preload_libraries TO 'a,b','c'`, `"a,b"`},
		{`SET session_preload_libraries TO 'a"b','c'`, `"a""b"`},
	} {
		statements, err := Parse(tc.query)
		if err != nil || len(statements) != 1 {
			t.Fatal(tc.query, statements, err)
		}
		s := statements[0].(*Set)
		if len(s.Values) != 2 || s.Values[0] != tc.first {
			t.Fatal(tc.query, s.Values)
		}
	}
}
