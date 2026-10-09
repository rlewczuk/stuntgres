package reference_test

import (
	"testing"
)

func TestReferenceSequenceSignatures(t *testing.T) {
	p := newReferenceScenario(t)
	for _, backend := range p.backends() {
		t.Run(backend.name, func(t *testing.T) {
			c := sequenceCoreConn(t, p.ctx, backend.db)
			sequenceProbeExec(t, p.ctx, c, "CREATE SEQUENCE s")
			for _, tc := range []struct{ q, signature string }{{"SELECT nextval()", "nextval()"}, {"SELECT lastval(1)", "lastval(integer)"}, {"SELECT setval('s')", "setval(unknown)"}, {"SELECT nextval(true)", "nextval(boolean)"}, {"SELECT setval('s',true)", "setval(unknown, boolean)"}} {
				want := controlsReferenceDiagnostic("42883", "function "+tc.signature+" does not exist")
				want.Hint = "No function matches the given name and argument types. You might need to add explicit type casts."
				want.Position = "8"
				sequenceProbeError(t, p.ctx, c, tc.q, &want)
				stmt, err := c.PrepareContext(p.ctx, tc.q)
				if stmt != nil {
					stmt.Close()
					t.Fatal("failed signature Prepare exposed statement")
				}
				referenceAssertDiagnostic(t, err, &want)
			}
			referenceRunStep(t, p.ctx, c, backend.recorder, sequenceInt64Step(referenceStep{query: "SELECT nextval('s')", selectRows: true, columns: []string{"nextval"}, types: []string{"INT8"}, rows: [][]any{{int64(1)}}}))
		})
	}
}
