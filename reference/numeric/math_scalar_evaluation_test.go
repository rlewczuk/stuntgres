package reference_test

import (
	"stuntgres/reference/fixture"
	"testing"
)

func TestReferenceMathScalarEvaluationPhase(t *testing.T) {
	p := fixture.NewScenario(t)
	p.Step(t, fixture.Exec("CREATE TABLE m(n NUMERIC,i INTEGER)", 0))
	s := fixture.ImplicitError("SELECT div(1,0) FROM m", fixture.Diagnostic{Code: "22012", Message: "division by zero"})
	s.SelectRows = true
	s.ErrorStage = "execute"
	p.Step(t, s)
	p.Step(t, fixture.Prepared(s))
	for _, tc := range []struct{ query, code, message string }{
		{"SELECT factorial(-1) FROM m", "22003", "factorial of a negative number is undefined"},
		{"SELECT exp(5999.9999999999999999999999) FROM m", "22003", "value overflows numeric format"},
	} {
		failure := fixture.ImplicitError(tc.query, fixture.Diagnostic{Code: tc.code, Message: tc.message})
		failure.ErrorStage = "execute"
		p.Step(t, failure)
		p.Step(t, fixture.Prepared(failure))
	}
	p.Step(t, fixture.Exec("INSERT INTO m VALUES(NULL,9)", 1))
	for _, q := range []string{"SELECT div(NULL,div(i,0)) FROM m", "SELECT div(n,0) FROM m"} {
		s := fixture.Select(q, []string{"div"}, []string{"NUMERIC"}, [][]any{{nil}})
		p.Step(t, s)
		p.Step(t, fixture.Prepared(s))
	}
	combined := fixture.Select("SELECT div(NULL,div(i,0)),div(n,0) FROM m", []string{"div", "div"}, []string{"NUMERIC", "NUMERIC"}, [][]any{{nil, nil}})
	p.Step(t, combined)
	p.Step(t, fixture.Prepared(combined))
	s = fixture.ImplicitError("SELECT div(n,div(i,0)) FROM m", fixture.Diagnostic{Code: "22012", Message: "division by zero"})
	s.SelectRows = true
	s.ErrorStage = "execute"
	p.Step(t, s)
	p.Step(t, fixture.Prepared(s))
	s.SelectRows = false
	p.Step(t, s)
}
