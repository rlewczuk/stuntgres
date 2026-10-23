package parser

import (
	"testing"
	"unicode/utf8"
)

func TestPLpgSQLCursorSourceSpans(t *testing.T) {
	source := "DECLARE c CURSOR(a integer,\"Odd 名\" integer) FOR\n SELECT /*é中*/ a;\nBEGIN\nOPEN\n c(\"Odd 名\"=> /* lead */ (7 /*内*/ + 1) /* tail */, a=>3);\nOPEN u FOR EXECUTE /* lead */ ('SELECT $1') /* tail */ USING /* lead */ (2 /*中*/ + 1) /* tail */, 'é中';\nOPEN u FOR\n SELECT /*é中*/ 7 /* tail */;\nFETCH ABSOLUTE /* lead */ (n /*内*/ + 1) /* tail */ FROM u INTO x;\nMOVE FORWARD /* lead */ n /* tail */ FROM u;\nFETCH FIRST FROM u INTO x;\nFETCH LAST FROM u INTO x;\nEND"
	raw, err := ParsePLpgSQL(source, PLReturnPolicy{})
	if err != nil {
		t.Fatal(err)
	}
	check := func(name string, start, end, delimiter Position, want string) {
		t.Helper()
		if got := PLExpressionSource(source, start, end); got != want {
			t.Fatalf("%s source got %q want %q", name, got, want)
		}
		if start.Character != utf8.RuneCountInString(source[:start.Offset])+1 || end.Character != utf8.RuneCountInString(source[:end.Offset])+1 || delimiter.Offset < end.Offset || delimiter.Character < end.Character {
			t.Fatalf("%s byte/character/token-end ledger: %+v %+v %+v", name, start, end, delimiter)
		}
	}
	bound := raw.Declarations[0].Cursor
	check("declaration query", bound.QueryStart, bound.QueryEnd, bound.QueryDelimiter, "SELECT /*é中*/ a")
	open := raw.Statements[0].Cursor
	if open.Arguments[0].Name.Text != "Odd 名" || open.Arguments[1].Name.Text != "a" {
		t.Fatal("named actual identities changed")
	}
	for i, want := range []string{"(7 /*内*/ + 1)", "3"} {
		actual := open.Arguments[i]
		check("actual", actual.Start, actual.End, actual.Delimiter, want)
	}
	if raw.Statements[0].Pos.Offset >= open.Variable.SourcePosition().Offset {
		t.Fatal("action origin collapsed into multiline operand")
	}
	dynamic := raw.Statements[1].Cursor
	check("command", dynamic.CommandStart, dynamic.CommandEnd, dynamic.CommandDelimiter, "('SELECT $1')")
	for i, want := range []string{"(2 /*中*/ + 1)", "'é中'"} {
		value := dynamic.Using[i]
		check("USING", value.Start, value.End, value.Delimiter, want)
	}
	query := raw.Statements[2].Cursor
	check("OPEN query", query.QueryStart, query.QueryEnd, query.QueryDelimiter, "SELECT /*é中*/ 7")
	for i, want := range []string{"(n /*内*/ + 1)", "n", "FIRST", "LAST"} {
		motion := raw.Statements[i+3].Cursor.Motion
		check("motion", motion.CountStart, motion.CountEnd, motion.CountDelimiter, want)
	}
}

func TestPLpgSQLCursorTargetSourceSpan(t *testing.T) {
	source := "BEGIN FETCH ABSOLUTE 1 FROM c INTO /* lead */ r.f1, a[(1 /*内*/ + 1)] /* tail */; END"
	raw, err := ParsePLpgSQL(source, PLReturnPolicy{})
	if err != nil {
		t.Fatal(err)
	}
	cursor := raw.Statements[0].Cursor
	if got := PLExpressionSource(source, cursor.TargetsStart, cursor.TargetsEnd); got != "r.f1, a[(1 /*内*/ + 1)]" {
		t.Fatal("target-list original token source", got)
	}
	if cursor.TargetsStart.Character != utf8.RuneCountInString(source[:cursor.TargetsStart.Offset])+1 || cursor.TargetsEnd.Character != utf8.RuneCountInString(source[:cursor.TargetsEnd.Offset])+1 || cursor.TargetsDelimiter.Offset < cursor.TargetsEnd.Offset {
		t.Fatal("target-list byte/character boundaries")
	}
}
