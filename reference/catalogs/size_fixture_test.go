package reference_test

import (
	"database/sql"
	"stuntgres/reference/fixture"
	"testing"
)

const sizeMissingHint = "No function matches the given name and argument types. You might need to add explicit type casts."
const sizeAmbiguousHint = "Could not choose a best candidate function. You might need to add explicit type casts."
const sizeUnitHint = `Valid units are "bytes", "B", "kB", "MB", "GB", "TB", and "PB".`

func sizeScenario(t *testing.T, body func(*testing.T, *fixture.Pair, fixture.Backend, *sql.Conn)) {
	t.Helper()
	p := fixture.NewScenario(t)
	for _, b := range p.Backends() {
		t.Run(b.Name, func(t *testing.T) {
			c, err := b.DB.Conn(p.Ctx)
			if err != nil {
				t.Fatal(err)
			}
			defer c.Close()
			body(t, p, b, c)
		})
	}
}

func sizeBoth(t *testing.T, p *fixture.Pair, b fixture.Backend, x fixture.Executor, s fixture.Step, analysisError bool) {
	t.Helper()
	for _, prepared := range []bool{false, true} {
		t.Run(map[bool]string{false: "direct", true: "prepared"}[prepared], func(t *testing.T) {
			step := s
			step.Prepared = prepared
			if step.Diagnostic != nil {
				step.SelectRows, step.ErrorStage = true, "execute"
				if prepared && analysisError {
					step.ErrorStage = "prepare"
				}
			}
			fixture.RunStep(t, p.Ctx, x, b.Recorder, step)
		})
	}
}

func sizeDiagnostic(code, message, hint, position string) fixture.Diagnostic {
	return fixture.Diagnostic{Severity: "ERROR", SeverityUnlocalized: "ERROR", Code: code, Message: message, Hint: hint, Position: position}
}

func sizeInvalid(input, unit string) fixture.Diagnostic {
	d := sizeDiagnostic("22023", `invalid size: "`+input+`"`, "", "")
	if unit != "" {
		d.Detail = `Invalid size unit: "` + unit + `".`
		d.Hint = sizeUnitHint
	}
	return d
}
