package reference_test

import (
	"testing"

	"stuntgres/reference/fixture"
)

func pgWaitEventsDiagnostics(t *testing.T, p *fixture.Pair, b fixture.Backend) {
	t.Helper()
	for _, tc := range []struct{ query, message, position string }{
		{"SELECT missing FROM pg_catalog.pg_wait_events", `column "missing" does not exist`, "8"},
		{"SELECT missing FROM pg_catalog.pg_wait_events WHERE FALSE", `column "missing" does not exist`, "8"},
		{"SELECT /*é*/ missing FROM pg_catalog.pg_wait_events", `column "missing" does not exist`, "14"},
		{"SELECT oid FROM pg_catalog.pg_wait_events", `column "oid" does not exist`, "8"},
	} {
		for _, prepared := range []bool{false, true} {
			s := fixture.ImplicitError(tc.query, fixture.Diagnostic{Code: "42703", Message: tc.message, Position: tc.position})
			s.SelectRows, s.Prepared, s.ErrorStage = true, prepared, "execute"
			if prepared {
				s.ErrorStage = "prepare"
			}
			fixture.RunStep(t, p.Ctx, b.DB, b.Recorder, s)
		}
	}
}

func TestReferencePGWaitEventsDiagnostics(t *testing.T) {
	pgWaitEventsScenario(t, pgWaitEventsDiagnostics)
}
