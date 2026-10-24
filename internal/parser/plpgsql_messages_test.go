package parser

import (
	"reflect"
	"strings"
	"testing"
)

func TestPLpgSQLRaiseParser(t *testing.T) {
	for _, tc := range []struct{ body string; level PLRaiseLevel; form PLRaiseForm }{
		{"RAISE '';", PLRaiseException, PLRaiseFormat},
		{"RAISE DEBUG '%%';", PLRaiseDebug, PLRaiseFormat},
		{"RAISE LOG division_by_zero;", PLRaiseLog, PLRaiseCondition},
		{"RAISE INFO SQLSTATE 'ZX987';", PLRaiseInfo, PLRaiseSQLState},
		{"RAISE NOTICE USING MESSAGE='x';", PLRaiseNotice, PLRaiseUsing},
		{"RAISE WARNING 'x';", PLRaiseWarning, PLRaiseFormat},
		{"RAISE EXCEPTION 'x';", PLRaiseException, PLRaiseFormat},
	} {
		t.Run(tc.body, func(t *testing.T) {
			raw, err := ParsePLpgSQL("BEGIN "+tc.body+" END", PLReturnPolicy{})
			if err != nil { t.Fatal(err) }
			got := raw.Statements[0]
			if got.Raise == nil || got.Raise.Level != tc.level || got.Raise.Form != tc.form || got.Rethrow || got.Expression != nil || got.Assert != nil { t.Fatalf("wrong RAISE variant: %#v", got) }
		})
	}
	const source = "BEGIN\n-- é\nRAISE NOTICE '% %',coalesce(1,2),(SELECT ARRAY[3,4]) USING DETAIL := ('using'),HINT=coalesce('a','b'),DETAIL='again'; RAISE; END"
	raw, err := ParsePLpgSQL(source, PLReturnPolicy{})
	if err != nil { t.Fatal(err) }
	raise := raw.Statements[0].Raise
	if len(raise.Arguments) != 2 || len(raise.Options) != 3 || raise.Options[0].Kind != PLRaiseDetail || raise.Options[1].Kind != PLRaiseHint || raise.Options[2].Kind != PLRaiseDetail || !raw.Statements[1].Rethrow { t.Fatal("lost ordered occurrences or rethrow") }
	for i, want := range []string{"coalesce(1,2)", "(SELECT ARRAY[3,4])"} {
		value := raise.Arguments[i]
		if got := PLExpressionSource(source, value.Start, value.End); got != want || value.Delimiter.Offset < value.End.Offset { t.Fatalf("argument source %q want %q", got, want) }
	}
	for i, want := range []string{"('using')", "coalesce('a','b')", "'again'"} {
		value := raise.Options[i].Value
		if got := PLExpressionSource(source, value.Start, value.End); got != want { t.Fatalf("option source %q want %q", got, want) }
	}
	fresh, err := ParsePLpgSQL(source, PLReturnPolicy{})
	if err != nil || !reflect.DeepEqual(raw, fresh) { t.Fatal("per-call grammar state", err) }
	fresh.Statements[0].Raise.Arguments[0] = PLExpression{}
	fresh.Statements[0].Raise.Options[0].Kind = PLRaiseSchema
	if raw.Statements[0].Raise.Arguments[0].Expression == nil || raw.Statements[0].Raise.Options[0].Kind != PLRaiseDetail { t.Fatal("parser results share mutable slices") }
	for _, body := range []string{"RAISE SQLSTATE 'abcd1';", "RAISE USING bogus='x';", "RAISE NOTICE '%',1,;", "RAISE NOTICE 'x' USING DETAIL=;", "RAISE 'x' junk;", "RAISE EXCEPTION;"} {
		if _, err := ParsePLpgSQL("BEGIN "+body+" END", PLReturnPolicy{}); err == nil { t.Fatal("accepted malformed RAISE", body) }
	}
	if _, err := Parse("SELECT raise, assert, notice, sqlstate FROM t"); err != nil { t.Fatal("changed ordinary SQL keywords", err) }
	if _, err := ParsePLpgSQL(`DECLARE "notice" integer:=7; "using" integer:=8; BEGIN RAISE NOTICE '%',("notice"+"using"); END`, PLReturnPolicy{}); err != nil { t.Fatal("quoted delimiter names", err) }
}

func TestPLpgSQLAssertParser(t *testing.T) {
	const source = "BEGIN\n-- é\nASSERT (coalesce(NULL,true)),coalesce('a','b'); ASSERT false; ASSERT NULL,NULL; END"
	raw, err := ParsePLpgSQL(source, PLReturnPolicy{})
	if err != nil { t.Fatal(err) }
	if len(raw.Statements) != 3 || raw.Statements[0].Assert == nil || raw.Statements[1].Assert.Message.Expression != nil || raw.Statements[2].Assert.Message.Expression == nil { t.Fatal("optional message confused with NULL") }
	first := raw.Statements[0]
	if first.Pos.Offset != strings.Index(source, "ASSERT") || first.Pos.Character != first.Pos.Offset || PLExpressionSource(source, first.Assert.Condition.Start, first.Assert.Condition.End) != "(coalesce(NULL,true))" || PLExpressionSource(source, first.Assert.Message.Start, first.Assert.Message.End) != "coalesce('a','b')" { t.Fatal("lost UTF8/action/operand coordinates", first) }
	for _, body := range []string{"ASSERT;", "ASSERT true,;", "ASSERT true,'x','y';", "ASSERT true junk;", "ASSERT true USING MESSAGE='x';"} {
		if _, err := ParsePLpgSQL("BEGIN "+body+" END", PLReturnPolicy{}); err == nil { t.Fatal("accepted malformed ASSERT", body) }
	}
	if _, err := ParsePLpgSQL("BEGIN ASSERT true; END; junk", PLReturnPolicy{}); err == nil { t.Fatal("accepted incomplete suffix") }
	if _, err := ParsePLpgSQL(`DECLARE "assert" boolean:=true; BEGIN ASSERT "assert"; END`, PLReturnPolicy{}); err != nil { t.Fatal(err) }
}
