package reference_test

import (
	"database/sql"
	"testing"
)

const sizeMissingHint = "No function matches the given name and argument types. You might need to add explicit type casts."
const sizeAmbiguousHint = "Could not choose a best candidate function. You might need to add explicit type casts."
const sizeUnitHint = `Valid units are "bytes", "B", "kB", "MB", "GB", "TB", and "PB".`

func sizeScenario(t *testing.T, body func(*testing.T, *referencePair, referenceBackend, *sql.Conn)) {
	t.Helper()
	p := newReferenceScenario(t)
	for _, b := range p.backends() {
		t.Run(b.name, func(t *testing.T) {
			c, err := b.db.Conn(p.ctx)
			if err != nil {
				t.Fatal(err)
			}
			defer c.Close()
			body(t, p, b, c)
		})
	}
}

func sizeBoth(t *testing.T, p *referencePair, b referenceBackend, x referenceExecutor, s referenceStep, analysisError bool) {
	t.Helper()
	for _, prepared := range []bool{false, true} {
		t.Run(map[bool]string{false: "direct", true: "prepared"}[prepared], func(t *testing.T) {
			step := s
			step.prepared = prepared
			if step.diagnostic != nil {
				step.selectRows, step.errorStage = true, "execute"
				if prepared && analysisError {
					step.errorStage = "prepare"
				}
			}
			referenceRunStep(t, p.ctx, x, b.recorder, step)
		})
	}
}

func sizeDiagnostic(code, message, hint, position string) referenceDiagnostic {
	return referenceDiagnostic{Severity: "ERROR", SeverityUnlocalized: "ERROR", Code: code, Message: message, Hint: hint, Position: position}
}

func sizeInvalid(input, unit string) referenceDiagnostic {
	d := sizeDiagnostic("22023", `invalid size: "`+input+`"`, "", "")
	if unit != "" {
		d.Detail = `Invalid size unit: "` + unit + `".`
		d.Hint = sizeUnitHint
	}
	return d
}
