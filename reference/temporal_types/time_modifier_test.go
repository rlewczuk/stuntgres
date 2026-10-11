package reference_test

import (
	"strings"
	"stuntgres/reference/fixture"
	"testing"
)

func TestReferenceTimeModifierDiagnostics(t *testing.T) {
	p := fixture.NewScenario(t)
	for _, backend := range p.Backends() {
		t.Run(backend.Name, func(t *testing.T) {
			run := func(s fixture.Step) { fixture.TemporalRunStep(t, p.Ctx, backend.DB, backend.Recorder, s) }
			for _, tc := range []struct{ q, code, message, position string }{
				{"CREATE TABLE bad(v TIME(-1))", "42601", "syntax error at or near \"-\"", "25"},
				{`CREATE TABLE bad(v "time"(-1))`, "22023", "TIME(-1) precision must not be negative", "20"},
				{`CREATE TABLE bad(v "time"(+1))`, "42601", "type modifiers must be simple constants or identifiers", "20"},
				{`CREATE TABLE bad(v "time"(1.5))`, "22P02", "invalid input syntax for type integer: \"1.5\"", "20"},
				{`CREATE TABLE bad(v "time"(2147483648))`, "22003", "value \"2147483648\" is out of range for type integer", "20"},
			} {
				for _, prepared := range []bool{false, true} {
					s := fixture.ImplicitError(tc.q, fixture.Diagnostic{Code: tc.code, Message: tc.message, Position: tc.position})
					s.Prepared = prepared
					s.ErrorStage = "execute"
					if strings.Contains(tc.q, "TIME(-1)") && prepared {
						s.ErrorStage = "prepare"
					}
					run(s)
				}
				run(fixture.Exec("CREATE TABLE bad(v TIME)", 0))
				run(fixture.Exec("DROP TABLE bad", 0))
			}
		})
	}
}
