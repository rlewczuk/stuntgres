package reference_test

import "testing"

func TestReferenceBetweenComparisonPrecedence(t *testing.T) {
	p := betweenReferenceSetup(t)
	for _, tc := range []struct{ query, position string }{
		{"SELECT TRUE BETWEEN 1<2 AND 2<3 FROM t WHERE id=1", "13"},
		{"SELECT id FROM t WHERE (TRUE BETWEEN 1<2 AND 2<3) AND id=1", "30"},
	} {
		step := referenceImplicitError(tc.query, referenceDiagnostic{Code: "42883", Message: "operator does not exist: boolean <= integer", Position: tc.position, Hint: "No operator matches the given name and argument types. You might need to add explicit type casts."})
		step.selectRows = true
		step.errorStage = "execute"
		scalarStep(t, p, step)
		step.errorStage = "prepare"
		scalarStep(t, p, referencePrepared(step))
	}
}
