package fixture

import (
	"database/sql"
	"testing"
)

func IntervalAvgDiagnostic(t *testing.T, p *Pair, conn *sql.Conn, recorder *Recorder, query string, want Diagnostic, args ...any) {
	t.Helper()
	for _, prepared := range []bool{false, true} {
		s := ImplicitError(query, want, args...)
		s.SelectRows = true
		s.Prepared = prepared
		s.ErrorStage = "execute"
		if prepared {
			s.ErrorStage = "prepare"
		}
		RunStep(t, p.Ctx, conn, recorder, s)
	}
}
