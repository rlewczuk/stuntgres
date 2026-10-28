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
	// The predefined-role consumer is parsed normally; privileges and the
	// publisher connection boundary belong to execution, not syntax exclusion.
	query := "CREATE SUBSCRIPTION guarded CONNECTION 'host=invalid password=unused' PUBLICATION unavailable WITH (connect=false,slot_name=NONE)"
	statements, err := Parse(query)
	if err != nil || len(statements) != 1 {
		t.Fatalf("supported subscription syntax: %#v / %v", statements, err)
	}
	sub, ok := statements[0].(*Subscription)
	if !ok || sub.Action != "create" || sub.Name.Text != "guarded" || sub.Connection != "host=invalid password=unused" || len(sub.Publications) != 1 || sub.Publications[0].Text != "unavailable" || len(sub.Options) != 2 || sub.Options[0].Name.Text != "connect" || sub.Options[1].Name.Text != "slot_name" {
		t.Fatalf("subscription AST: %#v", statements[0])
	}
	for _, query := range []string{`CREATE TABLE "checkpoint" ("subscription" INT)`, `SELECT "checkpoint" FROM "subscription"`} {
		statements, err := Parse(query)
		if err != nil || len(statements) != 1 {
			t.Fatal("quoted names became exclusions", query, statements, err)
		}
	}
}
