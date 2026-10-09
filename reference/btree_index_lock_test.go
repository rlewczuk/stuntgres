package reference_test

import (
	"database/sql"
	"testing"
)

func TestReferenceBtreeIndexBuildTimeouts(t *testing.T) {
	p := newReferenceScenario(t)
	referenceBtreeStreams(t, p, func(t *testing.T, db *sql.DB, recorder *referenceRecorder) {
		a, b := referenceLockConnections(t, p.ctx, db)
		referenceLockExec(t, p.ctx, a, "CREATE TABLE t(n INT)", 0)
		tx, err := a.BeginTx(p.ctx, nil)
		if err != nil {
			t.Fatal(err)
		}
		defer tx.Rollback()
		referenceLockExec(t, p.ctx, tx, "LOCK TABLE t IN ROW EXCLUSIVE MODE", 0)
		for _, tc := range []struct{ setting, code, message string }{
			{"lock_timeout", "55P03", "canceling statement due to lock timeout"},
			{"statement_timeout", "57014", "canceling statement due to statement timeout"},
		} {
			referenceLockExec(t, p.ctx, b, "SET "+tc.setting+"='20ms'", 0)
			step := referencePrepared(referenceImplicitError("CREATE INDEX idx ON t(n)", referenceDiagnostic{Code: tc.code, Message: tc.message}))
			step.errorStage = "execute"
			referenceRunStep(t, p.ctx, b, recorder, step)
			referenceLockExec(t, p.ctx, b, "SET "+tc.setting+"=0", 0)
		}
		if err := tx.Rollback(); err != nil {
			t.Fatal(err)
		}
		referenceRunStep(t, p.ctx, b, recorder, referenceSelect("SELECT relname FROM pg_catalog.pg_class WHERE relname='idx'", []string{"relname"}, []string{"NAME"}, nil))
		referenceRunStep(t, p.ctx, b, recorder, referenceExec("CREATE INDEX idx ON t(n)", 0))
	})
}
