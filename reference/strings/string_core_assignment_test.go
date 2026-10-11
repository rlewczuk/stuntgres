package reference_test

import (
	"stuntgres/reference/fixture"
	"testing"
)

func TestReferenceStringCoreAssignmentInferenceBoundary(t *testing.T) {
	p := fixture.NewScenario(t)
	for _, backend := range p.Backends() {
		t.Run(backend.Name, func(t *testing.T) {
			run := func(s fixture.Step) { fixture.RunStep(t, p.Ctx, backend.DB, backend.Recorder, s) }
			run(fixture.Exec("CREATE TABLE sc_resolution(v TEXT,n INT)", 0))
			run(fixture.Exec("INSERT INTO sc_resolution VALUES('sentinel',7)", 1))
			for _, tc := range []struct {
				q      string
				args   []any
				detail string
			}{
				{"INSERT INTO sc_resolution(v,n) VALUES($1,$1)", []any{"42"}, "text versus integer"},
				{"INSERT INTO sc_resolution(n,v) VALUES($1,$1)", []any{42}, "integer versus text"},
			} {
				diagnostic := fixture.Diagnostic{Code: "0A000", Message: "parameter reused with different destination types"}
				if backend.PG {
					diagnostic = fixture.Diagnostic{Code: "42P08", Message: "inconsistent types deduced for parameter $1", Detail: tc.detail, Position: "42"}
				}
				step := fixture.ImplicitError(tc.q, diagnostic, tc.args...)
				run(step)
				step.ErrorStage = "prepare"
				run(fixture.Prepared(step))
				run(fixture.Select("SELECT v,n FROM sc_resolution", []string{"v", "n"}, []string{"TEXT", "INT4"}, [][]any{{"sentinel", int64(7)}}))
			}
		})
	}
}
