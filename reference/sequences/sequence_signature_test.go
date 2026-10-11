package reference_test

import (
	"stuntgres/reference/fixture"
	"testing"
)

func TestReferenceSequenceSignatures(t *testing.T) {
	p := fixture.NewScenario(t)
	for _, backend := range p.Backends() {
		t.Run(backend.Name, func(t *testing.T) {
			c := fixture.SequenceCoreConn(t, p.Ctx, backend.DB)
			fixture.SequenceProbeExec(t, p.Ctx, c, "CREATE SEQUENCE s")
			for _, tc := range []struct{ q, signature string }{{"SELECT nextval()", "nextval()"}, {"SELECT lastval(1)", "lastval(integer)"}, {"SELECT setval('s')", "setval(unknown)"}, {"SELECT nextval(true)", "nextval(boolean)"}, {"SELECT setval('s',true)", "setval(unknown, boolean)"}} {
				want := fixture.ControlsReferenceDiagnostic("42883", "function "+tc.signature+" does not exist")
				want.Hint = "No function matches the given name and argument types. You might need to add explicit type casts."
				want.Position = "8"
				fixture.SequenceProbeError(t, p.Ctx, c, tc.q, &want)
				stmt, err := c.PrepareContext(p.Ctx, tc.q)
				if stmt != nil {
					stmt.Close()
					t.Fatal("failed signature Prepare exposed statement")
				}
				fixture.AssertDiagnostic(t, err, &want)
			}
			fixture.RunStep(t, p.Ctx, c, backend.Recorder, fixture.SequenceInt64Step(fixture.Step{Query: "SELECT nextval('s')", SelectRows: true, Columns: []string{"nextval"}, Types: []string{"INT8"}, Rows: [][]any{{int64(1)}}}))
		})
	}
}
