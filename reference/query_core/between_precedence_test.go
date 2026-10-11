package reference_test

import (
	"stuntgres/reference/fixture"
	"testing"
)

func TestReferenceBetweenComparisonPrecedence(t *testing.T) {
	p := betweenReferenceSetup(t)
	for _, tc := range []struct{ query, position string }{
		{"SELECT TRUE BETWEEN 1<2 AND 2<3 FROM t WHERE id=1", "13"},
		{"SELECT id FROM t WHERE (TRUE BETWEEN 1<2 AND 2<3) AND id=1", "30"},
	} {
		step := fixture.ImplicitError(tc.query, fixture.Diagnostic{Code: "42883", Message: "operator does not exist: boolean <= integer", Position: tc.position, Hint: "No operator matches the given name and argument types. You might need to add explicit type casts."})
		step.SelectRows = true
		step.ErrorStage = "execute"
		fixture.ScalarStep(t, p, step)
		step.ErrorStage = "prepare"
		fixture.ScalarStep(t, p, fixture.Prepared(step))
	}
}
