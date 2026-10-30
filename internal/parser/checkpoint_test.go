package parser

import (
	"errors"
	"reflect"
	"testing"

	"stuntgres/internal/sqlstate"
)

func TestCheckpointSyntax(t *testing.T) {
	for _, tc := range []struct{ q string; offset, character int }{
		{"CHECKPOINT", 0, 1}, {"cHeCkPoInT", 0, 1},
		{" /* before */ CHECKPOINT /* after */; ;", 14, 15},
	} {
		q := tc.q
		stmts, err := Parse(q)
		if err != nil || len(stmts) != 1 { t.Fatalf("%q: %#v %v", q, stmts, err) }
		s, ok := stmts[0].(*Checkpoint)
		if !ok || s.Pos.Offset != tc.offset || s.Pos.Character != tc.character { t.Fatalf("checkpoint AST: %#v", stmts[0]) }
	}
	for _, q := range []string{"CREATE TABLE checkpoint(checkpoint INT)", "SELECT checkpoint FROM checkpoint", `SELECT "checkpoint" FROM "checkpoint"`} {
		if stmts, err := Parse(q); err != nil || len(stmts) != 1 { t.Fatalf("identifier %q: %v", q, err) }
	}
	for _, tc := range []struct{ q, token, pos string }{
		{"CHECKPOINT x", "x", "12"}, {"CHECKPOINT (fast)", "(", "12"},
		{"CHECKPOINT IMMEDIATE", "IMMEDIATE", "12"}, {"CHECKPOINT FORCE", "FORCE", "12"},
		{"CHECKPOINT $1", "$1", "12"}, {"CHECKPOINT 1", "1", "12"},
		{"EXPLAIN CHECKPOINT", "CHECKPOINT", "9"}, {"PREPARE p AS CHECKPOINT", "CHECKPOINT", "14"},
		{"CHECKPOINT; SELECT )", ")", "20"},
	} {
		stmts, err := Parse(tc.q)
		var got *sqlstate.Error
		want := &sqlstate.Error{Severity:"ERROR", SeverityUnlocalized:"ERROR", Code:"42601", Message:`syntax error at or near "`+tc.token+`"`, Position:tc.pos}
		if stmts != nil || !errors.As(err, &got) || !reflect.DeepEqual(got, want) { t.Fatalf("%q: %#v %v want %#v", tc.q, got, err, want) }
	}
}
