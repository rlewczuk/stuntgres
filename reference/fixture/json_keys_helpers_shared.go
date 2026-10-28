package fixture

import (
	"fmt"
	"testing"
)

func JSONKeysBoth(t *testing.T, p *Pair, b Backend, c Executor, s Step) {
	t.Helper()
	for _, prepared := range []bool{false, true} {
		t.Run(fmt.Sprintf("prepared=%t", prepared), func(t *testing.T) {
			s.Prepared = prepared
			RunStep(t, p.Ctx, c, b.Recorder, s)
		})
	}
}

// Error expectations are complete pinned PostgreSQL 18.0 logical records.
func JSONKeysError(t *testing.T, p *Pair, b Backend, c Executor, q string, prepared bool, want Diagnostic, stage string, args ...any) {
	t.Helper()
	s := ErrorDiagnostic(q, want, args...)
	s.SelectRows = true
	s.Prepared = prepared
	s.ErrorStage = stage
	RunStep(t, p.Ctx, c, b.Recorder, s)
}
