package reference_test

import (
	"stuntgres/reference/fixture"
	"testing"
)

func TestReferenceRandomDiagnostics(t *testing.T) {
	p := fixture.NewScenario(t)
	p.Step(t, fixture.Exec("CREATE TABLE signatures(f DOUBLE PRECISION,s SMALLINT)", 0))
	for _, tc := range []struct{ q, code, message, hint, pos string }{
		{"SELECT random(f,f) FROM signatures", "42883", "function random(double precision, double precision) does not exist", "No function matches the given name and argument types. You might need to add explicit type casts.", "8"},
		{"SELECT random('1','1')", "42725", "function random(unknown, unknown) is not unique", "Could not choose a best candidate function. You might need to add explicit type casts.", "8"},
		{"SELECT random(s,s) FROM signatures", "42725", "function random(smallint, smallint) is not unique", "Could not choose a best candidate function. You might need to add explicit type casts.", "8"},
		{"SELECT random($1,$2)", "42725", "function random(unknown, unknown) is not unique", "Could not choose a best candidate function. You might need to add explicit type casts.", "8"},
		{"/*é*/ SELECT random(1)", "42883", "function random(integer) does not exist", "No function matches the given name and argument types. You might need to add explicit type casts.", "14"},
		{"SELECT random_normal(1,TRUE)", "42883", "function random_normal(integer, boolean) does not exist", "No function matches the given name and argument types. You might need to add explicit type casts.", "8"},
	} {
		for _, query := range []bool{false, true} {
			step := fixture.ImplicitError(tc.q, fixture.Diagnostic{Code: tc.code, Message: tc.message, Hint: tc.hint, Position: tc.pos})
			step.SelectRows = query
			step.ErrorStage = "execute"
			for _, backend := range p.Backends() {
				direct := step
				if backend.PG && tc.q == "SELECT random($1,$2)" {
					want := fixture.ControlsReferenceDiagnostic("42P02", "there is no parameter $1")
					want.Position = "15"
					direct.State, direct.Diagnostic = want.Code, &want
				}
				fixture.RunStep(t, p.Ctx, backend.DB, backend.Recorder, direct)
				step.ErrorStage = "prepare"
				fixture.RunStep(t, p.Ctx, backend.DB, backend.Recorder, fixture.Prepared(step))
				step.ErrorStage = "execute"
			}
		}
	}
}
