package reference_test

import (
	"database/sql"
	"strings"
	"testing"

	"stuntgres/reference/fixture"
)

// Default/nullability scenarios share the existing dual-mode domain fixture.
func domainDefaultScenario(t *testing.T, body func(*testing.T, *fixture.Pair, fixture.Backend, *sql.Conn, *fixture.Recorder)) {
	t.Helper()
	domainReferenceScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn, r *fixture.Recorder, _ *[]fixture.Diagnostic) {
		body(t, p, b, c, r)
	})
}

// Complete diagnostics/stages observed against pinned PostgreSQL 18.6.
func domainNotNullDiagnostic(schema, display string) fixture.Diagnostic {
	d := fixture.ControlsReferenceDiagnostic("23502", "domain "+schema+"."+display+" does not allow null values")
	d.Schema, d.DataType = schema, strings.Trim(display, `"`)
	return d
}

func domainDefaultFailure(q string, d fixture.Diagnostic, prepared, rows bool, args ...any) fixture.Step {
	s := fixture.ErrorDiagnostic(q, d, args...)
	s.Prepared, s.SelectRows, s.ErrorStage = prepared, rows, "execute"
	return s
}
