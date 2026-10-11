package parser

import (
	"errors"
	"reflect"
	"testing"

	"stuntgres/internal/sqlstate"
)

func TestPredefinedUnavailableSyntax(t *testing.T) {
	for _, tc := range []struct{ query, message, position string }{
		{"CHECKPOINT", "statement is unsupported", "1"},
		{"CREATE SUBSCRIPTION guarded CONNECTION 'host=invalid password=unused' PUBLICATION unavailable WITH (connect=false,slot_name=NONE)", "CREATE syntax is unsupported", "8"},
		{"COPY t TO '/unavailable'", "statement is unsupported", "1"},
		{"VACUUM t", "statement is unsupported", "1"},
		{"ANALYZE t", "statement is unsupported", "1"},
		{"CLUSTER t", "statement is unsupported", "1"},
		{"REFRESH MATERIALIZED VIEW t", "statement is unsupported", "1"},
	} {
		statements, err := Parse(tc.query)
		var diagnostic *sqlstate.Error
		want := &sqlstate.Error{Severity: "ERROR", SeverityUnlocalized: "ERROR", Code: "0A000", Message: tc.message, Position: tc.position}
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
