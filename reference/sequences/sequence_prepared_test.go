package reference_test

import (
	"stuntgres/reference/fixture"
	"testing"
)

func TestReferenceSequencePreparedInvalidation(t *testing.T) {
	for _, query := range []string{"SELECT nextval('s')", "SELECT nextval('s'::regclass)"} {
		t.Run(query, func(t *testing.T) {
			p := fixture.NewScenario(t)
			for _, backend := range p.Backends() {
				t.Run(backend.Name, func(t *testing.T) {
					w := newSequencePreparedSession(t, p, backend)
					w.exec("CREATE SEQUENCE s START 14 CACHE 1")
					held := w.hold(query)
					w.exec("ALTER SEQUENCE s RENAME TO identity")
					w.failure(held, "42P01", `relation "s" does not exist`, "16")
					missing, err := w.c.PrepareContext(p.Ctx, query)
					if missing != nil {
						missing.Close()
						t.Fatal("missing-name Prepare exposed a statement")
					}
					want := fixture.ControlsReferenceDiagnostic("42P01", `relation "s" does not exist`)
					want.Position = "16"
					fixture.AssertDiagnostic(t, err, &want)
					parameter := w.hold("SELECT nextval($1)")
					w.held(parameter, fixture.SequenceInt64Step(fixture.Step{Args: []any{"identity"}, SelectRows: true, Columns: []string{"nextval"}, Types: []string{"INT8"}, Rows: [][]any{{int64(14)}}}))
					w.exec("CREATE SEQUENCE s START 70")
					w.value(held, 70)
					fixture.SequenceProbeValue(t, p.Ctx, w.c, "SELECT nextval('identity')", 15)
				})
			}
		})
	}
}
