package core_test

import (
	"errors"
	"testing"

	"stuntgres/internal/sqlstate"
)

func lockModesEngineDenied(t *testing.T, err error, message string) {
	t.Helper()
	var d *sqlstate.Error
	want := sqlstate.Error{Severity: "ERROR", SeverityUnlocalized: "ERROR", Code: "42501", Message: message}
	if !errors.As(err, &d) || *d != want {
		t.Fatalf("got %#v (%v), want %#v", d, err, want)
	}
}
