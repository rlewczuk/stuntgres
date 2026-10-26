package parser

import (
	"errors"
	"strconv"
	"strings"
	"testing"
	"unicode/utf8"

	"stuntgres/internal/sqlstate"
)

func TestRoutineProcedureOptionShapes(t *testing.T) {
	position := func(source, token string) Position {
		at := strings.Index(source, token)
		if at < 0 {
			t.Fatalf("missing token %q in %q", token, source)
		}
		return Position{Offset: at, Character: utf8.RuneCountInString(source[:at]) + 1}
	}
	for _, option := range []string{
		"STRICT", "IMMUTABLE", "STABLE", "VOLATILE", "LEAKPROOF", "WINDOW",
		"NOT LEAKPROOF", "CALLED ON NULL INPUT", "RETURNS NULL ON NULL INPUT",
		"COST 1", "COST -1.5", "COST +2e3", "ROWS 1", "ROWS +2", "ROWS -1.5",
		"SUPPORT pc_support", "SUPPORT public.pc_support", `SUPPORT "cost"."support"`,
		"PARALLEL SAFE", "PARALLEL arbitrary", `PARALLEL "strict"`, "PARALLEL integer",
	} {
		for _, form := range []string{
			"%s LANGUAGE SQL AS 'SELECT 1'",
			"LANGUAGE SQL %s AS 'SELECT 1'",
			"LANGUAGE SQL AS 'SELECT 1' %s",
			"AS 'SELECT 1' %s LANGUAGE SQL",
			"AS 'SELECT 1' LANGUAGE SQL %s",
			"%s BEGIN ATOMIC SELECT 1; END",
			"LANGUAGE SQL %s BEGIN ATOMIC SELECT 1; END",
		} {
			clause := strings.Replace(form, "%s", option, 1)
			source := "/* café 雪 */ CREATE OR REPLACE PROCEDURE pc_bad(OUT answer integer) " + clause
			statements, err := Parse(source)
			if err != nil || len(statements) != 1 {
				t.Fatal(source, statements, err)
			}
			d, ok := statements[0].(*CreateFunction)
			if !ok || !d.Procedure || !d.Replace || d.ResultSpecified || len(d.Arguments) != 1 || d.Arguments[0].Mode != "out" || d.Arguments[0].Name.Text != "answer" || d.Language.Text != "sql" {
				t.Fatal("procedure declaration lost", source, d)
			}
			if d.InvalidAttribute != position(source, option) {
				t.Fatal("complete option introducer position lost", source, d.InvalidAttribute)
			}
			if strings.Contains(form, "ATOMIC") {
				if len(d.Standard) != 1 || d.Body != "" {
					t.Fatal("standard body lost", source, d)
				}
			} else if d.Body != "SELECT 1" || d.Standard != nil {
				t.Fatal("quoted body lost", source, d)
			}
		}
	}
	const prefix = "/* café 雪 */ CREATE PROCEDURE pc_bad() LANGUAGE SQL AS 'SELECT 1'"
	for _, tc := range []struct{ suffix, at string }{
		{" nonsense", "nonsense"}, {" 123", "123"},
		{" COST", ""}, {" ROWS", ""}, {" SUPPORT", ""}, {" PARALLEL", ""},
		{" NOT", ""}, {" CALLED ON NULL", ""}, {" RETURNS NULL ON NULL", ""},
		{" COST nope", "nope"}, {" ROWS nope", "nope"},
		{" CALLED NULL INPUT", "NULL"}, {" RETURNS integer", "integer"},
		{" COST 1 nonsense", "nonsense"}, {" STRICT nonsense", "nonsense"},
		{` "strict"`, `"strict"`}, {` "cost" 1`, `"cost"`},
		{` "called" ON NULL INPUT`, `"called"`}, {` NOT "leakproof"`, `"leakproof"`},
		{` CALLED ON NULL "input"`, `"input"`},
	} {
		source := prefix + tc.suffix
		statements, err := Parse(source)
		var diagnostic *sqlstate.Error
		wantPosition := utf8.RuneCountInString(source) + 1
		wantMessage := "syntax error at end of input"
		if tc.at != "" {
			wantPosition = position(source, tc.at).Character
			wantMessage = `syntax error at or near "` + tc.at + `"`
		}
		if len(statements) != 0 || !errors.As(err, &diagnostic) || diagnostic.Code != "42601" || diagnostic.Position != strconv.Itoa(wantPosition) || diagnostic.Message != wantMessage {
			t.Fatal("malformed option must fail the whole parse at its token/EOF", source, statements, err)
		}
	}
	for _, source := range []string{
		prefix + " STRICT; invalid",
		prefix + "; CREATE PROCEDURE other() LANGUAGE SQL AS 'SELECT 1' nonsense",
		prefix + " nonsense; SELECT 1",
	} {
		if statements, err := Parse(source); err == nil || len(statements) != 0 {
			t.Fatal("published a valid prefix", source, statements, err)
		}
	}
	source := "CREATE PROCEDURE p() COST 1 LANGUAGE SQL STRICT AS 'SELECT 1' VOLATILE; CREATE PROCEDURE q() LANGUAGE SQL AS 'SELECT 2'; SELECT called,cost,support,parallel,strict FROM names"
	statements, err := Parse(source)
	if err != nil || len(statements) != 3 {
		t.Fatal("options leaked into later statements or ordinary names", statements, err)
	}
	first := statements[0].(*CreateFunction)
	second := statements[1].(*CreateFunction)
	if first.InvalidAttribute != position(source, "COST") || second.InvalidAttribute != (Position{}) {
		t.Fatal("first complete introducer or per-declaration ownership lost", first, second)
	}
	for _, source := range []string{
		"CREATE FUNCTION f() RETURNS integer STRICT LANGUAGE SQL VOLATILE AS 'SELECT 1'",
		"CREATE FUNCTION f() RETURNS integer AS 'SELECT 1' STRICT LANGUAGE SQL VOLATILE",
		"CREATE FUNCTION f() RETURNS SETOF integer STRICT LANGUAGE SQL VOLATILE BEGIN ATOMIC SELECT 1; END",
	} {
		statements, err := Parse(source)
		if err != nil || len(statements) != 1 {
			t.Fatal(source, statements, err)
		}
		d := statements[0].(*CreateFunction)
		if !d.Strict || d.Volatility != "volatile" || d.Procedure {
			t.Fatal("function attributes changed", d)
		}
	}
}
