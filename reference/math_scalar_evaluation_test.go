package reference_test

import "testing"

func TestReferenceMathScalarEvaluationPhase(t *testing.T) {
	p := newReferenceScenario(t)
	p.step(t, referenceExec("CREATE TABLE m(n NUMERIC,i INTEGER)", 0))
	s := referenceImplicitError("SELECT div(1,0) FROM m", referenceDiagnostic{Code: "22012", Message: "division by zero"})
	s.selectRows = true
	s.errorStage = "execute"
	p.step(t, s)
	p.step(t, referencePrepared(s))
	for _, tc := range []struct{ query, code, message string }{
		{"SELECT factorial(-1) FROM m", "22003", "factorial of a negative number is undefined"},
		{"SELECT exp(5999.9999999999999999999999) FROM m", "22003", "value overflows numeric format"},
	} {
		failure := referenceImplicitError(tc.query, referenceDiagnostic{Code: tc.code, Message: tc.message})
		failure.errorStage = "execute"
		p.step(t, failure)
		p.step(t, referencePrepared(failure))
	}
	p.step(t, referenceExec("INSERT INTO m VALUES(NULL,9)", 1))
	for _, q := range []string{"SELECT div(NULL,div(i,0)) FROM m", "SELECT div(n,0) FROM m"} {
		s := referenceSelect(q, []string{"div"}, []string{"NUMERIC"}, [][]any{{nil}})
		p.step(t, s)
		p.step(t, referencePrepared(s))
	}
	combined := referenceSelect("SELECT div(NULL,div(i,0)),div(n,0) FROM m", []string{"div", "div"}, []string{"NUMERIC", "NUMERIC"}, [][]any{{nil, nil}})
	p.step(t, combined)
	p.step(t, referencePrepared(combined))
	s = referenceImplicitError("SELECT div(n,div(i,0)) FROM m", referenceDiagnostic{Code: "22012", Message: "division by zero"})
	s.selectRows = true
	s.errorStage = "execute"
	p.step(t, s)
	p.step(t, referencePrepared(s))
	s.selectRows = false
	p.step(t, s)
}
