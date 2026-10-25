package engine

import (
	"errors"
	"testing"

	"stuntgres/internal/sqlstate"
)

func waitsDiagnostic(t *testing.T, err error, code, message string, position ...string) {
	t.Helper()
	var got *sqlstate.Error
	want := sqlstate.Error{Severity: "ERROR", SeverityUnlocalized: "ERROR", Code: code, Message: message}
	if len(position) != 0 {
		want.Position = position[0]
	}
	if !errors.As(err, &got) || *got != want {
		t.Fatalf("diagnostic = %#v (%v), want %#v", got, err, want)
	}
}
