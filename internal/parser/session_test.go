package parser

import (
	"errors"
	"reflect"
	"stuntgres/internal/sqlstate"
	"testing"
)

func TestSessionGrammar(t *testing.T) {
	for _, tc := range []struct {
		query string
		want  Statement
	}{
		{"SET application_name TO DEFAULT", &Set{Name: Identifier{Text: "application_name", Pos: Position{Offset: 4, Character: 5}}, Default: true, Pos: Position{Character: 1}}},
		{"SET application_name TO 'DEFAULT'", &Set{Name: Identifier{Text: "application_name", Pos: Position{Offset: 4, Character: 5}}, Values: []string{"DEFAULT"}, Pos: Position{Character: 1}}},
		{"SHOW application_name", &Show{Name: Identifier{Text: "application_name", Pos: Position{Offset: 5, Character: 6}}, Pos: Position{Character: 1}}},
		{`SET application_name TO E'a\nb'`, &Set{Name: Identifier{Text: "application_name", Pos: Position{Offset: 4, Character: 5}}, Values: []string{"a\nb"}, Pos: Position{Character: 1}}},
	} {
		stmts, err := Parse(tc.query)
		if err != nil || len(stmts) != 1 || !reflect.DeepEqual(stmts[0], tc.want) {
			t.Fatalf("%q %+v %v", tc.query, stmts, err)
		}
	}
	for _, q := range []string{"SET SESSION application_name = true", "SET application_name TO +00042,-00042,MixedCase", "SHOW \"application_name\"", `CREATE TABLE set(show TEXT,session INT,"to" BOOL)`, "SELECT show FROM set", `SET application_name TO "to"`} {
		if _, err := Parse(q); err != nil {
			t.Fatalf("%q: %v", q, err)
		}
	}
	for _, tc := range []struct{ query, position, token string }{{"SET application_name TO NULL", "25", "NULL"}, {"SET application_name TO $1", "25", "$1"}, {"SET application_name TO TO", "25", "TO"}, {"CREATE TABLE invalid_to(to BOOL)", "25", "to"}} {
		_, err := Parse(tc.query)
		var diagnostic *sqlstate.Error
		if !errors.As(err, &diagnostic) || diagnostic.Code != "42601" || diagnostic.Message != `syntax error at or near "`+tc.token+`"` || diagnostic.Position != tc.position {
			t.Fatalf("%q: %#v (%v)", tc.query, diagnostic, err)
		}
	}
	for _, q := range []string{"SET application_name TO 1.5", "SET application_name TO 1e2", "SHOW application_name ignored", "SET application_name TO x; nonsense"} {
		if _, err := Parse(q); err == nil {
			t.Fatalf("accepted unsupported %q", q)
		}
	}
}

func TestSessionDecimalIntegerBoundary(t *testing.T) {
	for _, scalar := range []string{"1_000", "0x2a", "0O_1_2", "0b0", "0x7FFF_FFFF"} {
		for _, sign := range []string{"", "+", "-"} {
			query := "SET application_name TO " + sign + scalar
			_, err := Parse(query)
			position := "25"
			if sign != "" {
				position = "26"
			}
			var got *sqlstate.Error
			want := &sqlstate.Error{Severity: "ERROR", SeverityUnlocalized: "ERROR", Code: "0A000", Message: "non-decimal setting integers are unsupported", Position: position}
			if !errors.As(err, &got) || !reflect.DeepEqual(got, want) {
				t.Fatalf("%q: %#v want %#v (%v)", query, got, want, err)
			}
		}
	}
	for _, text := range []string{"1_000", "0x2a"} {
		stmts, err := Parse("SET application_name TO '" + text + "'")
		if err != nil || len(stmts) != 1 {
			t.Fatal(stmts, err)
		}
		got := stmts[0].(*Set)
		if !reflect.DeepEqual(got.Values, []string{text}) {
			t.Fatalf("quoted value changed: %v", got.Values)
		}
	}
}
