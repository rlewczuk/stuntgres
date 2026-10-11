package reference_test

import (
	"database/sql"
	"stuntgres/reference/fixture"
	"testing"
)

func TestReferenceDatetimeZoneInvalidSelectedCasts(t *testing.T) {
	datetimeZoneScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
		// Pinned PostgreSQL18.0 confirms these absent-cast diagnostics and
		// character positions; no additional cast providers are admitted.
		for _, tc := range []struct {
			query, message, position string
		}{
			{"SELECT date_add(TIMESTAMPTZ '2004-01-01+00',TIMETZ '12:00+00'::INTERVAL) AS v FROM tz_witness", "cannot cast type time with time zone to interval", "62"},
			{"SELECT date_add(TIMESTAMPTZ '2004-01-01+00',1::INTERVAL) AS v FROM tz_witness", "cannot cast type integer to interval", "46"},
		} {
			t.Run(tc.message, func(t *testing.T) {
				want := fixture.ControlsReferenceDiagnostic("42846", tc.message)
				want.Position = tc.position
				step := fixture.ImplicitError(tc.query, want)
				step.SelectRows, step.ErrorStage = true, "execute"
				fixture.RunStep(t, p.Ctx, c, b.Recorder, step)
				step.Prepared, step.ErrorStage = true, "prepare"
				fixture.RunStep(t, p.Ctx, c, b.Recorder, step)
			})
		}
	})
}
