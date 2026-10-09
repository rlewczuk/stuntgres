package reference_test

import (
	"strings"
	"testing"
)

func TestReferenceTimeModifierDiagnostics(t *testing.T) {
	p := newReferenceScenario(t)
	for _, backend := range p.backends() {
		t.Run(backend.name, func(t *testing.T) {
			run := func(s referenceStep) { referenceTemporalRunStep(t, p.ctx, backend.db, backend.recorder, s) }
			for _, tc := range []struct{ q, code, message, position string }{
				{"CREATE TABLE bad(v TIME(-1))", "42601", "syntax error at or near \"-\"", "25"},
				{`CREATE TABLE bad(v "time"(-1))`, "22023", "TIME(-1) precision must not be negative", "20"},
				{`CREATE TABLE bad(v "time"(+1))`, "42601", "type modifiers must be simple constants or identifiers", "20"},
				{`CREATE TABLE bad(v "time"(1.5))`, "22P02", "invalid input syntax for type integer: \"1.5\"", "20"},
				{`CREATE TABLE bad(v "time"(2147483648))`, "22003", "value \"2147483648\" is out of range for type integer", "20"},
			} {
				for _, prepared := range []bool{false, true} {
					s := referenceImplicitError(tc.q, referenceDiagnostic{Code: tc.code, Message: tc.message, Position: tc.position})
					s.prepared = prepared
					s.errorStage = "execute"
					if strings.Contains(tc.q, "TIME(-1)") && prepared {
						s.errorStage = "prepare"
					}
					run(s)
				}
				run(referenceExec("CREATE TABLE bad(v TIME)", 0))
				run(referenceExec("DROP TABLE bad", 0))
			}
		})
	}
}
