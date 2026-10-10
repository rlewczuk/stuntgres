package reference_test

import (
	"database/sql"
	"testing"
)

func TestReferenceDatetimeZoneInvalidSelectedCasts(t *testing.T) {
	datetimeZoneScenario(t, func(t *testing.T, p *referencePair, b referenceBackend, c *sql.Conn) {
		// Pinned PostgreSQL18.0 confirms these absent-cast diagnostics and
		// character positions; no additional cast providers are admitted.
		for _, tc := range []struct {
			query, message, position string
		}{
			{"SELECT date_add(TIMESTAMPTZ '2004-01-01+00',TIMETZ '12:00+00'::INTERVAL) AS v FROM tz_witness", "cannot cast type time with time zone to interval", "62"},
			{"SELECT date_add(TIMESTAMPTZ '2004-01-01+00',1::INTERVAL) AS v FROM tz_witness", "cannot cast type integer to interval", "46"},
		} {
			t.Run(tc.message, func(t *testing.T) {
				want := controlsReferenceDiagnostic("42846", tc.message)
				want.Position = tc.position
				step := referenceImplicitError(tc.query, want)
				step.selectRows, step.errorStage = true, "execute"
				referenceRunStep(t, p.ctx, c, b.recorder, step)
				step.prepared, step.errorStage = true, "prepare"
				referenceRunStep(t, p.ctx, c, b.recorder, step)
			})
		}
	})
}
