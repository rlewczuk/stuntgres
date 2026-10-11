package reference_test

import (
	"database/sql"
	"stuntgres/reference/fixture"
	"testing"
)

func TestReferenceBtreeIndexBuildTimeouts(t *testing.T) {
	p := fixture.NewScenario(t)
	referenceBtreeStreams(t, p, func(t *testing.T, db *sql.DB, recorder *fixture.Recorder) {
		a, b := fixture.LockConnections(t, p.Ctx, db)
		fixture.LockExec(t, p.Ctx, a, "CREATE TABLE t(n INT)", 0)
		tx, err := a.BeginTx(p.Ctx, nil)
		if err != nil {
			t.Fatal(err)
		}
		defer tx.Rollback()
		fixture.LockExec(t, p.Ctx, tx, "LOCK TABLE t IN ROW EXCLUSIVE MODE", 0)
		for _, tc := range []struct{ setting, code, message string }{
			{"lock_timeout", "55P03", "canceling statement due to lock timeout"},
			{"statement_timeout", "57014", "canceling statement due to statement timeout"},
		} {
			fixture.LockExec(t, p.Ctx, b, "SET "+tc.setting+"='20ms'", 0)
			step := fixture.Prepared(fixture.ImplicitError("CREATE INDEX idx ON t(n)", fixture.Diagnostic{Code: tc.code, Message: tc.message}))
			step.ErrorStage = "execute"
			fixture.RunStep(t, p.Ctx, b, recorder, step)
			fixture.LockExec(t, p.Ctx, b, "SET "+tc.setting+"=0", 0)
		}
		if err := tx.Rollback(); err != nil {
			t.Fatal(err)
		}
		fixture.RunStep(t, p.Ctx, b, recorder, fixture.Select("SELECT relname FROM pg_catalog.pg_class WHERE relname='idx'", []string{"relname"}, []string{"NAME"}, nil))
		fixture.RunStep(t, p.Ctx, b, recorder, fixture.Exec("CREATE INDEX idx ON t(n)", 0))
	})
}
