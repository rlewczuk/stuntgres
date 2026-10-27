package parser

import (
	"context"
	"errors"
	"fmt"
	"reflect"
	"strconv"
	"strings"
	"sync"
	"testing"
	"unicode/utf8"

	"stuntgres/internal/sqlstate"
)

func TestSettingsGrammar(t *testing.T) {
	for _, q := range []string{"RESET application_name", "RESET ALL", "SHOW ALL", "SET demo.deep.value TO '3.5'", "SHOW demo.deep.value", "SELECT current_setting($1)", "SELECT current_setting(NULL)", "SELECT current_setting('a',true)", "SELECT current_setting($1,$2)", "SELECT current_setting('a',NULL)", "SELECT set_config($1,$2,$3)"} {
		s, err := Parse(q)
		if err != nil || len(s) != 1 {
			t.Errorf("%s: %v %v", q, s, err)
		}
	}
	// Signature and coercion errors belong to ordinary function binding, not
	// syntax; invalid calls must parse just like composed or qualified calls.
	for _, q := range []string{"SELECT current_setting()", "SELECT current_setting('a',true,false)", "SELECT set_config('a','b')", "SELECT current_setting(true)"} {
		if _, err := Parse(q); err != nil {
			t.Errorf("%s: %v", q, err)
		}
	}
	for _, tc := range []struct {
		query                  string
		start, name, arg, cast int
	}{
		{"SELECT current_setting($1::TEXT)", 8, 8, 24, 26},
		{"SELECT pg_catalog.current_setting($1::TEXT)", 8, 19, 35, 37},
		{"/*é*/ SELECT pg_catalog.current_setting(a.k)", 14, 25, 41, 0},
	} {
		statements, err := Parse(tc.query)
		if err != nil || len(statements) != 1 {
			t.Fatal(tc.query, statements, err)
		}
		selecting, ok := statements[0].(*Select)
		if !ok || len(selecting.Targets) != 1 {
			t.Fatal("setting call is not an ordinary SELECT", tc.query, statements)
		}
		call, ok := selecting.Targets[0].(FunctionExpr)
		if !ok || call.Pos.Character != tc.start || call.Name.Parts[len(call.Name.Parts)-1].Pos.Character != tc.name {
			t.Fatal("setting call lost component positions", tc.query, call)
		}
		if len(call.Args) != 1 || call.Args[0].SourcePosition().Character != tc.arg {
			t.Fatal("setting call lost argument position", tc.query, call)
		}
		if tc.cast != 0 {
			cast, ok := call.Args[0].(CastExpr)
			if !ok || cast.Syntax != PostfixCast || cast.Pos.Character != tc.cast || cast.Child.SourcePosition().Character != tc.arg {
				t.Fatal("setting call lost cast positions", tc.query, call)
			}
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
	for _, q := range []string{
		`SELECT current_setting('application_name') AS v`,
		`SELECT current_setting('application_name') || ':' || current_setting('search_path')`,
		`SELECT current_setting('application_name') FROM settings_input`,
		`SELECT pg_catalog.set_config('application_name',v,false) AS configured FROM settings_input`,
		`CREATE FUNCTION settings_body() RETURNS text LANGUAGE SQL BEGIN ATOMIC SELECT current_setting('application_name') AS v; END`,
	} {
		if _, err := Parse(q); err != nil {
			t.Errorf("%s: %v", q, err)
		}
	}
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

func TestSettingsCallOutputLabelsGrammar(t *testing.T) {
	position := func(query, token string) Position {
		offset := strings.LastIndex(query, token)
		if offset < 0 {
			t.Fatal("missing position witness", query, token)
		}
		return Position{Offset: offset, Character: utf8.RuneCountInString(query[:offset]) + 1}
	}
	for _, signature := range []struct {
		name, args string
		count      int
	}{
		{"current_setting", "$1::TEXT", 1},
		{"current_setting", "$1,$2", 2},
		{"set_config", "$1,$2,$3", 3},
	} {
		for _, schema := range []string{"", "pg_catalog."} {
			for _, label := range []struct{ suffix, text, token string }{
				{"", "", ""},
				{" AS value", "value", "value"},
				{" value", "value", "value"},
				{" AS MiXeD", "mixed", "MiXeD"},
				{` "Étiquette"`, "Étiquette", `"Étiquette"`},
				{` AS "a""b"`, `a"b`, `"a""b"`},
				{" AS from", "from", "from"},
				{" AS on", "on", "on"},
				{" AS day", "day", "day"},
				{" AS filter", "filter", "filter"},
				{` "filter"`, "filter", `"filter"`},
				{" select", "select", "select"},
				{" check", "check", "check"},
				{" like", "like", "like"},
				{" ilike", "ilike", "ilike"},
				{" similar", "similar", "similar"},
			} {
				query := "/*é*/ SELECT " + schema + signature.name + "(" + signature.args + ")" + label.suffix
				statements, err := Parse(query)
				if err != nil || len(statements) != 1 {
					t.Fatal(query, statements, err)
				}
				selecting, ok := statements[0].(*Select)
				if !ok || len(selecting.Targets) != 1 || len(selecting.Labels) != 1 {
					t.Fatalf("setting call is not an ordinary labeled SELECT: %s: %#v", query, statements[0])
				}
				call, ok := selecting.Targets[0].(FunctionExpr)
				if !ok || call.Pos != position(query, schema+signature.name) || len(call.Args) != signature.count {
					t.Fatalf("%s: %#v", query, selecting.Targets[0])
				}
				wantName := []Identifier{{Text: signature.name, Pos: position(query, signature.name)}}
				if schema != "" {
					wantName = append([]Identifier{{Text: "pg_catalog", Pos: position(query, "pg_catalog")}}, wantName...)
				}
				if !reflect.DeepEqual(call.Name.Parts, wantName) {
					t.Fatal("lost function/schema component positions", query, call.Name.Parts, wantName)
				}
				wantLabel := Identifier{}
				if label.token != "" {
					wantLabel = Identifier{Text: label.text, Pos: position(query, label.token)}
				}
				if selecting.Labels[0] != wantLabel {
					t.Fatal(query, selecting.Labels[0], wantLabel)
				}
				for i, arg := range call.Args {
					if signature.args == "$1::TEXT" {
						cast, ok := arg.(CastExpr)
						if !ok || cast.TypeName.Name != "text" || cast.Syntax != PostfixCast || cast.Pos != position(query, "::TEXT") {
							t.Fatal("lost operand cast", arg)
						}
						arg = cast.Child
					}
					p, ok := arg.(ParameterExpr)
					if !ok || p.Number != i+1 || p.Pos != position(query, "$"+strconv.Itoa(i+1)) {
						t.Fatal("lost parameter operand/ordinal", arg)
					}
				}
			}
		}
	}
	// These are ordinary query expressions, not specialized settings transport.
	// Preserve label/expression disambiguation on both column and setting bases.
	for _, base := range []string{"v", "current_setting($1)"} {
		for _, keyword := range []string{"like", "ilike", "similar"} {
			query := "SELECT " + base + " " + keyword + " FROM t"
			statements, err := Parse(query)
			if err != nil || len(statements) != 1 {
				t.Fatal(query, statements, err)
			}
			selecting := statements[0].(*Select)
			if len(selecting.Targets) != 1 || len(selecting.Labels) != 1 || selecting.Labels[0] != (Identifier{Text: keyword, Pos: position(query, keyword)}) || selecting.From == nil {
				t.Fatal("lost ordinary pattern label", query, selecting)
			}
			if base == "v" {
				if _, ok := selecting.Targets[0].(ColumnExpr); !ok {
					t.Fatal("label changed column target", query, selecting.Targets[0])
				}
			} else if _, ok := selecting.Targets[0].(FunctionExpr); !ok {
				t.Fatal("label changed setting target", query, selecting.Targets[0])
			}
		}
		for _, tc := range []struct {
			pattern, symbol string
			similar, escape bool
		}{
			{"LIKE 'x%'", "~~", false, false},
			{"ILIKE 'x%'", "~~*", false, false},
			{"NOT LIKE 'x%'", "!~~", false, false},
			{"NOT ILIKE 'x%'", "!~~*", false, false},
			{"LIKE 'x%' ESCAPE '!'", "~~", false, true},
			{"ILIKE 'x%' ESCAPE '!'", "~~*", false, true},
			{"NOT LIKE 'x%' ESCAPE '!'", "!~~", false, true},
			{"NOT ILIKE 'x%' ESCAPE '!'", "!~~*", false, true},
			{"SIMILAR TO 'x%'", "~", true, false},
			{"NOT SIMILAR TO 'x%'", "!~", true, false},
			{"SIMILAR TO 'x%' ESCAPE '!'", "~", true, true},
			{"NOT SIMILAR TO 'x%' ESCAPE '!'", "!~", true, true},
		} {
			query := "SELECT " + base + " " + tc.pattern + " FROM t"
			statements, err := Parse(query)
			if err != nil || len(statements) != 1 {
				t.Fatal(query, statements, err)
			}
			selecting := statements[0].(*Select)
			if len(selecting.Targets) != 1 || len(selecting.Labels) != 1 || selecting.Labels[0] != (Identifier{}) || selecting.From == nil {
				t.Fatal("expression acquired output label", query, selecting)
			}
			op, ok := selecting.Targets[0].(OperatorExpr)
			if !ok || len(op.Name.Parts) != 1 || op.Name.Parts[0].Text != tc.symbol || len(op.Args) != 2 || op.SQLSimilar != tc.similar || op.Pos != position(query, tc.pattern) {
				t.Fatal("lost ordinary pattern operator", query, selecting.Targets[0])
			}
			if base == "v" {
				if _, ok := op.Args[0].(ColumnExpr); !ok {
					t.Fatal("lost column operand", query, op.Args[0])
				}
			} else {
				call, ok := op.Args[0].(FunctionExpr)
				if !ok || len(call.Args) != 1 || call.Args[0].(ParameterExpr).Number != 1 {
					t.Fatal("lost setting operand", query, op.Args[0])
				}
			}
			right := op.Args[1]
			if tc.similar || tc.escape {
				helper, ok := right.(FunctionExpr)
				wantName := "like_escape"
				if tc.similar {
					wantName = "similar_to_escape"
				}
				wantSyntax := OrdinaryFunctionSyntax
				if tc.similar {
					wantSyntax = SimilarEscapeFunctionSyntax
				}
				wantArgs := []Expr{LiteralExpr{Value: Value{Kind: StringValue, Text: "x%", Pos: position(query, "'x%'")}}}
				if tc.escape {
					wantArgs = append(wantArgs, LiteralExpr{Value: Value{Kind: StringValue, Text: "!", Pos: position(query, "'!'")}})
				}
				wantHelper := FunctionExpr{Name: ObjectName{Parts: []Identifier{{Text: "pg_catalog", Pos: op.Pos}, {Text: wantName, Pos: op.Pos}}}, Args: wantArgs, Syntax: wantSyntax, Pos: op.Pos}
				if !ok || !reflect.DeepEqual(helper, wantHelper) {
					t.Fatal("lost pattern lowering", query, right)
				}
				right = helper.Args[0]
			}
			literal, ok := right.(LiteralExpr)
			if !ok || literal.Value.Text != "x%" || literal.SourcePosition() != position(query, "'x%'") {
				t.Fatal("lost pattern literal", query, right)
			}
		}
	}
	for _, query := range []string{
		"SELECT current_setting($1) AS value FROM relation",
		"SELECT current_setting($1) AS value, other",
	} {
		statements, err := Parse(query)
		if err != nil || len(statements) != 1 {
			t.Fatal("ordinary settings composition rejected", query, statements, err)
		}
		selecting := statements[0].(*Select)
		call, ok := selecting.Targets[0].(FunctionExpr)
		if !ok || len(call.Args) != 1 || call.Args[0].(ParameterExpr).Number != 1 || selecting.Labels[0].Text != "value" {
			t.Fatal("composition lost labeled setting operand", query, selecting)
		}
		if strings.Contains(query, " FROM ") {
			if selecting.From == nil || len(selecting.Targets) != 1 {
				t.Fatal("composition lost FROM", query, selecting)
			}
		} else if len(selecting.Targets) != 2 {
			t.Fatal("composition lost second target", query, selecting)
		}
	}
	for _, tc := range []struct{ query, column, token string }{
		{"SELECT current_setting($1) LIKE 'x%'", "", ""},
		{"SELECT pg_catalog.current_setting($1,$2) ILIKE 'x%'", "", ""},
		{"SELECT pg_catalog.set_config($1,$2,$3) SIMILAR TO 'x%'", "", ""},
		{"SELECT current_setting($1) LIKE value", "value", "value"},
		{`SELECT current_setting($1) LIKE "trailing"`, "trailing", `"trailing"`},
	} {
		query := tc.query
		statements, err := Parse(query)
		if err != nil || len(statements) != 1 {
			t.Fatal("ordinary pattern composition rejected", query, statements, err)
		}
		selecting := statements[0].(*Select)
		if len(selecting.Targets) != 1 || len(selecting.Labels) != 1 || selecting.Labels[0] != (Identifier{}) || selecting.From != nil {
			t.Fatal("ordinary pattern composition changed shape", query, selecting)
		}
		op, ok := selecting.Targets[0].(OperatorExpr)
		if !ok || len(op.Args) != 2 {
			t.Fatal("ordinary pattern composition lost operator", query, selecting.Targets[0])
		}
		if _, ok := op.Args[0].(FunctionExpr); !ok {
			t.Fatal("ordinary pattern composition lost setting operand", query, op.Args[0])
		}
		if tc.column != "" {
			if len(op.Name.Parts) != 1 || op.Name.Parts[0].Text != "~~" || op.Pos != position(query, "LIKE") {
				t.Fatal("ordinary pattern composition changed LIKE operator", query, op)
			}
			call := op.Args[0].(FunctionExpr)
			wantCall := FunctionExpr{
				Name: ObjectName{Parts: []Identifier{{Text: "current_setting", Pos: position(query, "current_setting")}}},
				Args: []Expr{ParameterExpr{Number: 1, Pos: position(query, "$1")}},
				Pos:  position(query, "current_setting"),
			}
			if !reflect.DeepEqual(call.Name, wantCall.Name) || !reflect.DeepEqual(call.Args, wantCall.Args) || call.Pos != wantCall.Pos {
				t.Fatal("ordinary pattern composition changed setting operand", query, call)
			}
			column, ok := op.Args[1].(ColumnExpr)
			wantColumn := ColumnExpr{
				Name: ObjectName{Parts: []Identifier{{Text: tc.column, Pos: position(query, tc.token)}}},
				Pos:  position(query, tc.token),
			}
			if !ok || !reflect.DeepEqual(column, wantColumn) {
				t.Fatal("ordinary pattern composition lost positioned column operand", query, op.Args[1])
			}
		}
	}
	{
		query := "SELECT current_setting($1) FILTER (WHERE true)"
		statements, err := Parse(query)
		if err != nil || len(statements) != 1 {
			t.Fatal("ordinary settings FILTER composition rejected", query, statements, err)
		}
		call, ok := statements[0].(*Select).Targets[0].(FunctionExpr)
		if !ok || len(call.Args) != 1 || call.Args[0].(ParameterExpr).Number != 1 || call.Filter == nil {
			t.Fatal("ordinary settings FILTER composition lost operands", query, call)
		}
	}
	// TRAILING is a reserved keyword even though it is admitted as a bare label;
	// unlike value or "trailing", it cannot be the LIKE operand's column name.
	{
		query := "SELECT current_setting($1) like trailing"
		statements, err := Parse(query)
		var diagnostic *sqlstate.Error
		if len(statements) != 0 || !errors.As(err, &diagnostic) || diagnostic.Code != "42601" || diagnostic.Message != `syntax error at or near "trailing"` || diagnostic.Position != strconv.Itoa(position(query, "trailing").Character) {
			t.Fatal("reserved pattern operand leaked AST or changed grammar diagnostic", query, statements, err)
		}
	}
	// FILTER keeps its existing table, column, aggregate and window consumers.
	for _, query := range []string{
		"CREATE TABLE filter(filter TEXT)", "SET filter TO 'x'",
		"SELECT filter FROM filter", "SELECT v AS filter FROM filter",
		`SELECT v "filter" FROM filter`,
		"SELECT filter(v) FROM filter",
		"SELECT pg_catalog.filter(v) FROM filter",
		"SELECT sum(v) FILTER (WHERE true) FROM filter",
		"SELECT sum(v) FILTER (WHERE true) OVER () FROM filter",
		"SELECT count(*) FILTER (WHERE true) FROM filter",
		"SELECT lower(v) FILTER (WHERE true) FROM filter",
		"SELECT sum(v) OVER filter FROM filter",
	} {
		if _, err := Parse(query); err != nil {
			t.Fatal("identifier factoring changed an existing consumer", query, err)
		}
	}
	for _, tc := range []struct{ suffix, code, message, token string }{
		{" AS", "42601", "syntax error at end of input", ""},
		{" AS 1", "42601", `syntax error at or near "1"`, "1"},
		{" day", "42601", `syntax error at or near "day"`, "day"},
		{" on", "42601", `syntax error at or near "on"`, "on"},
		{" FILTER /*é*/", "42601", "syntax error at end of input", ""},
		{" FILTER;", "42601", `syntax error at or near ";"`, ";"},
		{" FILTER (", "42601", "syntax error at end of input", ""},
		{" FROM /*é*/", "42601", "syntax error at end of input", ""},
		{" FROM;", "42601", `syntax error at or near ";"`, ";"},
		{" AS value FROM", "42601", "syntax error at end of input", ""},
		{` AS ""`, "42601", `zero-length delimited identifier at or near """"`, `""`},
		{" AS value trailing", "42601", `syntax error at or near "trailing"`, "trailing"},
		{" like )", "42601", `syntax error at or near ")"`, ")"},
		{" ilike )", "42601", `syntax error at or near ")"`, ")"},
		{" similar )", "42601", `syntax error at or near ")"`, ")"},
	} {
		query := "/*é*/ SELECT current_setting($1)" + tc.suffix
		statements, err := Parse(query)
		wantPos := utf8.RuneCountInString(query) + 1
		if tc.token != "" {
			wantPos = utf8.RuneCountInString(query[:strings.LastIndex(query, tc.token)]) + 1
		}
		var diagnostic *sqlstate.Error
		if len(statements) != 0 || !errors.As(err, &diagnostic) || diagnostic.Code != tc.code || diagnostic.Message != tc.message || diagnostic.Position != strconv.Itoa(wantPos) {
			t.Fatal("rejection leaked AST or changed grammar diagnostic", query, statements, err)
		}
	}
	for _, query := range []string{
		"SELECT set_config('application_name','x',false) AS value; SELECT )",
		"SELECT set_config('application_name','x',false) similar; SELECT )",
		"SELECT current_setting($1) AS value FROM )",
	} {
		statements, err := Parse(query)
		var diagnostic *sqlstate.Error
		if len(statements) != 0 || !errors.As(err, &diagnostic) || diagnostic.Position == "" {
			t.Fatal("accepted a valid prefix or lost positioned parser error", query, statements, err)
		}
	}
	batch, err := Parse("SELECT set_config('application_name','x',false) AS first; SELECT current_setting('application_name') AS second")
	if err != nil || len(batch) != 2 || batch[0].(*Select).Labels[0].Text != "first" || batch[1].(*Select).Labels[0].Text != "second" {
		t.Fatal("lost complete direct batch", batch, err)
	}
	query := `/*é*/ SELECT pg_catalog.set_config($1::TEXT,$2,$3) AS "Étiquette"`
	first, err := Parse(query)
	if err != nil {
		t.Fatal(err)
	}
	snapshot, err := Parse(query)
	if err != nil {
		t.Fatal(err)
	}
	var workers sync.WaitGroup
	failures := make(chan error, 8)
	for range 8 {
		workers.Add(1)
		go func() {
			defer workers.Done()
			for range 16 {
				s, err := Parse("SELECT current_setting($1,$2) AS replacement")
				if err != nil || len(s) != 1 || s[0].(*Select).Labels[0].Text != "replacement" {
					failures <- fmt.Errorf("independent parse: %v %v", s, err)
					return
				}
			}
		}()
	}
	workers.Wait()
	close(failures)
	for err := range failures {
		t.Error(err)
	}
	if !reflect.DeepEqual(first, snapshot) {
		t.Fatal("scratch reuse mutated retained setting syntax", first, snapshot)
	}
}
