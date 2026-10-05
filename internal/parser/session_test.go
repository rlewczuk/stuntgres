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
	for _, q := range []string{"SET LOCAL application_name TO x", "SET application_name TO 1.5", "SET application_name TO 1e2", "SET custom.name TO x", "SHOW ALL", "RESET application_name", "SHOW application_name ignored", "SET application_name TO x; nonsense"} {
		if _, err := Parse(q); err == nil {
			t.Fatalf("accepted unsupported %q", q)
		}
	}
}
