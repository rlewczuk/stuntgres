package reference_test

import "testing"

func TestReferenceBitwiseRuntimeNullStillEvaluatesRows(t *testing.T) {
	p := newReferenceScenario(t)
	p.step(t, referenceExec("CREATE TABLE runtime_null(x TEXT,i INTEGER)", 0))
	p.step(t, referenceExec("INSERT INTO runtime_null VALUES('bad',NULL)", 1))
	for _, query := range []string{
		"SELECT i & CAST(x AS integer) FROM runtime_null",
		"SELECT CAST(x AS integer) & i FROM runtime_null",
	} {
		step := referenceImplicitError(query, referenceDiagnostic{Code: "22P02", Message: `invalid input syntax for type integer: "bad"`})
		step.selectRows, step.errorStage = true, "execute"
		p.step(t, step)
		p.step(t, referencePrepared(step))
	}
}
