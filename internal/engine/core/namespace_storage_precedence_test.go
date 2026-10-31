package core_test

import (
	"errors"
	"testing"

	"stuntgres/internal/sqlstate"
)

func defaultUnitDiagnostic(t *testing.T, err error, code, message string) {
	t.Helper()
	var d *sqlstate.Error
	if !errors.As(err, &d) || d.Code != code || d.Message != message || d.Severity != "ERROR" || d.SeverityUnlocalized != "ERROR" {
		t.Fatalf("diagnostic = %#v; want %s %q", err, code, message)
	}
}
