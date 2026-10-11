package reference_test

import (
	"stuntgres/reference/fixture"
	"testing"
)

func TestReferenceBitwiseRuntimeNullStillEvaluatesRows(t *testing.T) {
	p := fixture.NewScenario(t)
	p.Step(t, fixture.Exec("CREATE TABLE runtime_null(x TEXT,i INTEGER)", 0))
	p.Step(t, fixture.Exec("INSERT INTO runtime_null VALUES('bad',NULL)", 1))
	for _, query := range []string{
		"SELECT i & CAST(x AS integer) FROM runtime_null",
		"SELECT CAST(x AS integer) & i FROM runtime_null",
	} {
		step := fixture.ImplicitError(query, fixture.Diagnostic{Code: "22P02", Message: `invalid input syntax for type integer: "bad"`})
		step.SelectRows, step.ErrorStage = true, "execute"
		p.Step(t, step)
		p.Step(t, fixture.Prepared(step))
	}
}
