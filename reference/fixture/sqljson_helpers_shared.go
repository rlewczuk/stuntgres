package fixture

import (
	"testing"
)

func SQLJSONBoth(t *testing.T, p *Pair, b Backend, c Executor, s Step) {
	t.Helper()
	for _, prepared := range []bool{false, true} {
		name := "direct"
		if prepared {
			name = "prepared"
		}
		t.Run(name, func(t *testing.T) {
			s.Prepared = prepared
			RunStep(t, p.Ctx, c, b.Recorder, s)
		})
	}
}

func SQLJSONDiagnostic(t *testing.T, p *Pair, b Backend, c Executor, q string, d Diagnostic, analysis bool, args ...any) {
	t.Helper()
	for _, prepared := range []bool{false, true} {
		name := "direct"
		if prepared {
			name = "prepared"
		}
		t.Run(name, func(t *testing.T) {
			s := ErrorDiagnostic(q, d, args...)
			s.SelectRows = true
			s.Prepared = prepared
			s.ErrorStage = "execute"
			if prepared && analysis {
				s.ErrorStage = "prepare"
			}
			RunStep(t, p.Ctx, c, b.Recorder, s)
		})
	}
}
