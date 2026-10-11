package reference_test

import (
	"stuntgres/reference/fixture"
	"testing"
)

func TestReferenceDatetimeOverlapsParameterDiagnostics(t *testing.T) {
	p := fixture.NewScenario(t)
	for _, b := range p.Backends() {
		t.Run(b.Name, func(t *testing.T) {
			c, err := b.DB.Conn(p.Ctx)
			if err != nil {
				t.Fatal(err)
			}
			defer c.Close()
			q := "SELECT ($1::timestamp,INTERVAL '1 day') OVERLAPS (TIMESTAMP '2000-01-01',TIMESTAMP '2000-01-02')"
			// Main's pinned PostgreSQL oracle observed statement1 Context on all three routes.
			d := fixture.ControlsReferenceDiagnostic("22008", "timestamp out of range")
			d.Context = "SQL function \"overlaps\" statement 1"
			datetimeOverlapsFailure(t, p, b, c, q, d, "294276-12-31")
			stmt, err := c.PrepareContext(p.Ctx, q)
			if err != nil {
				t.Fatal(err)
			}
			defer stmt.Close()
			held := fixture.HeldStatement{Executor: c, Statement: stmt, SQL: q}
			t.Run("held_overflow", func(t *testing.T) {
				s := fixture.ErrorDiagnostic(q, d, "294276-12-31")
				s.SelectRows = true
				s.ErrorStage = "execute"
				fixture.RunStep(t, p.Ctx, held, b.Recorder, s)
			})
			s := fixture.Select(q, []string{"overlaps"}, []string{"BOOL"}, [][]any{{true}}, "2000-01-01")
			s.CheckMetadata = datetimeOverlapsMetadata
			fixture.RunStep(t, p.Ctx, held, b.Recorder, s)
		})
	}
}
