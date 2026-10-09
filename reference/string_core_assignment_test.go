package reference_test

import "testing"

func TestReferenceStringCoreAssignmentInferenceBoundary(t *testing.T) {
	p := newReferenceScenario(t)
	for _, backend := range p.backends() {
		t.Run(backend.name, func(t *testing.T) {
			run := func(s referenceStep) { referenceRunStep(t, p.ctx, backend.db, backend.recorder, s) }
			run(referenceExec("CREATE TABLE sc_resolution(v TEXT,n INT)", 0))
			run(referenceExec("INSERT INTO sc_resolution VALUES('sentinel',7)", 1))
			for _, tc := range []struct {
				q      string
				args   []any
				detail string
			}{
				{"INSERT INTO sc_resolution(v,n) VALUES($1,$1)", []any{"42"}, "text versus integer"},
				{"INSERT INTO sc_resolution(n,v) VALUES($1,$1)", []any{42}, "integer versus text"},
			} {
				diagnostic := referenceDiagnostic{Code: "0A000", Message: "parameter reused with different destination types"}
				if backend.pg {
					diagnostic = referenceDiagnostic{Code: "42P08", Message: "inconsistent types deduced for parameter $1", Detail: tc.detail, Position: "42"}
				}
				step := referenceImplicitError(tc.q, diagnostic, tc.args...)
				run(step)
				step.errorStage = "prepare"
				run(referencePrepared(step))
				run(referenceSelect("SELECT v,n FROM sc_resolution", []string{"v", "n"}, []string{"TEXT", "INT4"}, [][]any{{"sentinel", int64(7)}}))
			}
		})
	}
}
