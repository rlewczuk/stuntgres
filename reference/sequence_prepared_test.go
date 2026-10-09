package reference_test

import "testing"

func TestReferenceSequencePreparedInvalidation(t *testing.T) {
	for _, query := range []string{"SELECT nextval('s')", "SELECT nextval('s'::regclass)"} {
		t.Run(query, func(t *testing.T) {
			p := newReferenceScenario(t)
			for _, backend := range p.backends() {
				t.Run(backend.name, func(t *testing.T) {
					w := newSequencePreparedSession(t, p, backend)
					w.exec("CREATE SEQUENCE s START 14 CACHE 1")
					held := w.hold(query)
					w.exec("ALTER SEQUENCE s RENAME TO identity")
					w.failure(held, "42P01", `relation "s" does not exist`, "16")
					missing, err := w.c.PrepareContext(p.ctx, query)
					if missing != nil {
						missing.Close()
						t.Fatal("missing-name Prepare exposed a statement")
					}
					want := controlsReferenceDiagnostic("42P01", `relation "s" does not exist`)
					want.Position = "16"
					referenceAssertDiagnostic(t, err, &want)
					parameter := w.hold("SELECT nextval($1)")
					w.held(parameter, sequenceInt64Step(referenceStep{args: []any{"identity"}, selectRows: true, columns: []string{"nextval"}, types: []string{"INT8"}, rows: [][]any{{int64(14)}}}))
					w.exec("CREATE SEQUENCE s START 70")
					w.value(held, 70)
					sequenceProbeValue(t, p.ctx, w.c, "SELECT nextval('identity')", 15)
				})
			}
		})
	}
}
