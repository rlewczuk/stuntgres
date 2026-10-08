package reference_test

import "testing"

func TestReferenceQueryLimitAnalysis(t *testing.T) {
	p := referenceLimitFixture(t)
	p.step(t, referenceExec("CREATE TABLE b(id INT,y BOOL)", 0))
	p.step(t, referenceExec("CREATE TABLE empty(id INT)", 0))
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
			step := referenceImplicitError(tc.q, referenceDiagnostic{Code: tc.code, Message: tc.message, Position: tc.position})
			step.selectRows = true
			step.errorStage = "execute"
			p.step(t, step)
			if !tc.runtime {
				step.errorStage = "prepare"
			}
			p.step(t, referencePrepared(step))
		})
	}
}
