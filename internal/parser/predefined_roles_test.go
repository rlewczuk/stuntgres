package parser

import (
	"errors"
	"reflect"
	"testing"

	"stuntgres/internal/sqlstate"
)

func TestPredefinedUnavailableSyntax(t *testing.T) {
	for _, tc := range []struct{ query, message, position, code string }{
		{"CHECKPOINT", "statement is unsupported", "1", "0A000"},
		{"CREATE SUBSCRIPTION guarded CONNECTION 'host=invalid password=unused' PUBLICATION unavailable WITH (connect=false,slot_name=NONE)", "CREATE syntax is unsupported", "8", "0A000"},
		{"CLUSTER t", "statement is unsupported", "1", "0A000"},
		{"CREATE VACUUM", `syntax error at or near "VACUUM"`, "8", "42601"},
	} {
		statements, err := Parse(tc.query)
		var diagnostic *sqlstate.Error
		want := &sqlstate.Error{Severity: "ERROR", SeverityUnlocalized: "ERROR", Code: tc.code, Message: tc.message, Position: tc.position}
		if statements != nil || !errors.As(err, &diagnostic) || !reflect.DeepEqual(diagnostic, want) {
			t.Fatalf("%s: %#v / %v want %#v", tc.query, diagnostic, err, want)
		}
	}
	for _, query := range []string{`CREATE TABLE "checkpoint" ("subscription" INT)`, `SELECT "checkpoint" FROM "subscription"`} {
		statements, err := Parse(query)
		if err != nil || len(statements) != 1 {
			t.Fatal("quoted names became exclusions", query, statements, err)
		}
	}
}
