package fixture

import (
	"fmt"
	"testing"
)

func BinaryAccessBodyError(t *testing.T, p *Pair, b Backend, c Executor, q, code, msg string) {
	t.Helper()
	for _, prepared := range []bool{false, true} {
		for _, query := range []bool{false, true} {
			s := ErrorDiagnostic(q, ControlsReferenceDiagnostic(code, msg))
			s.SelectRows = query
			s.ErrorStage = "execute"
			if prepared {
				s = Prepared(s)
			}
			RunStep(t, p.Ctx, c, b.Recorder, s)
		}
	}
}

func BinaryAccessAnalysisError(t *testing.T, p *Pair, b Backend, c Executor, q string, d Diagnostic) {
	t.Helper()
	BinaryAccessDiagnosticStages(t, p, b, c, q, d, d, "prepare")
}

func BinaryAccessDiagnosticStages(t *testing.T, p *Pair, b Backend, c Executor, q string, direct, prepared Diagnostic, preparedStage string) {
	t.Helper()
	direct.Severity = "ERROR"
	direct.SeverityUnlocalized = "ERROR"
	prepared.Severity = "ERROR"
	prepared.SeverityUnlocalized = "ERROR"
	for _, prefix := range []string{"", "/*é*/ "} {
		wantDirect, wantPrepared := direct, prepared
		if prefix != "" {
			for _, want := range []*Diagnostic{&wantDirect, &wantPrepared} {
				if want.Position != "" {
					var pos int
					fmt.Sscan(want.Position, &pos)
					want.Position = Position(pos + 6)
				}
			}
		}
		for _, query := range []bool{false, true} {
			s := ErrorDiagnostic(prefix+q, wantDirect)
			s.SelectRows = query
			s.ErrorStage = "execute"
			RunStep(t, p.Ctx, c, b.Recorder, s)
			s = ErrorDiagnostic(prefix+q, wantPrepared)
			s.SelectRows = query
			s.ErrorStage = preparedStage
			RunStep(t, p.Ctx, c, b.Recorder, Prepared(s))
		}
	}
}
