package reference_test

import "testing"

func TestReferenceDatetimeOverlapsParameterDiagnostics(t *testing.T) {
	p := newReferenceScenario(t)
	for _, b := range p.backends() {
		t.Run(b.name, func(t *testing.T) {
			c, err := b.db.Conn(p.ctx)
			if err != nil {
				t.Fatal(err)
			}
			defer c.Close()
			q := "SELECT ($1::timestamp,INTERVAL '1 day') OVERLAPS (TIMESTAMP '2000-01-01',TIMESTAMP '2000-01-02')"
			// Main's pinned PostgreSQL oracle observed statement1 Context on all three routes.
			d := controlsReferenceDiagnostic("22008", "timestamp out of range")
			d.Context = "SQL function \"overlaps\" statement 1"
			datetimeOverlapsFailure(t, p, b, c, q, d, "294276-12-31")
			stmt, err := c.PrepareContext(p.ctx, q)
			if err != nil {
				t.Fatal(err)
			}
			defer stmt.Close()
			held := referenceHeldStatement{referenceExecutor: c, statement: stmt, query: q}
			t.Run("held_overflow", func(t *testing.T) {
				s := referenceErrorDiagnostic(q, d, "294276-12-31")
				s.selectRows = true
				s.errorStage = "execute"
				referenceRunStep(t, p.ctx, held, b.recorder, s)
			})
			s := referenceSelect(q, []string{"overlaps"}, []string{"BOOL"}, [][]any{{true}}, "2000-01-01")
			s.checkMetadata = datetimeOverlapsMetadata
			referenceRunStep(t, p.ctx, held, b.recorder, s)
		})
	}
}
