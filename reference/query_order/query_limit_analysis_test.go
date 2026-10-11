package reference_test

import (
	"stuntgres/reference/fixture"
	"testing"
)

func TestReferenceQueryLimitAnalysis(t *testing.T) {
	p := referenceLimitFixture(t)
	p.Step(t, fixture.Exec("CREATE TABLE b(id INT,y BOOL)", 0))
	p.Step(t, fixture.Exec("CREATE TABLE empty(id INT)", 0))
	for _, tc := range []struct {
		q, code, message, position string
		runtime                    bool
	}{
		{"SELECT id FROM b LIMIT y", "42804", "argument of LIMIT must be type bigint, not type boolean", "24", false},
		{"SELECT id FROM t LIMIT CAST(NULL AS TEXT)", "42804", "argument of LIMIT must be type bigint, not type text", "29", false},
		{"SELECT id FROM t LIMIT CAST(NULL AS BOOLEAN)", "42804", "argument of LIMIT must be type bigint, not type boolean", "29", false},
		{"SELECT id FROM t WHERE FALSE LIMIT -1", "2201W", "LIMIT must not be negative", "", true},
		{"SELECT id FROM empty OFFSET -1", "2201X", "OFFSET must not be negative", "", true},
		{"SELECT id FROM empty LIMIT -1", "2201W", "LIMIT must not be negative", "", true},
	} {
		t.Run(tc.q, func(t *testing.T) {
			step := fixture.ImplicitError(tc.q, fixture.Diagnostic{Code: tc.code, Message: tc.message, Position: tc.position})
			step.SelectRows = true
			step.ErrorStage = "execute"
			p.Step(t, step)
			if !tc.runtime {
				step.ErrorStage = "prepare"
			}
			p.Step(t, fixture.Prepared(step))
		})
	}
}
