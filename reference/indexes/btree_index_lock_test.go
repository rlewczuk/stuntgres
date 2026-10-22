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
		// Parse/Describe are administrative, not the20ms Execute witness.
		const createSQL = "CREATE INDEX idx ON t(n)"
		create, err := b.PrepareContext(p.Ctx, createSQL)
		if err != nil {
			t.Fatal(err)
		}
		defer create.Close()
		held := fixture.HeldStatement{Executor: b, Statement: create, SQL: createSQL}
		resets := make(map[string]*sql.Stmt, 2)
		for _, setting := range []string{"lock_timeout", "statement_timeout"} {
			reset, err := b.PrepareContext(p.Ctx, "SET "+setting+"=0")
			if err != nil {
				t.Fatal(err)
			}
			defer reset.Close()
			resets[setting] = reset
		}
		identity := fixture.SettingsPhysical(t, b)
		state := func(indexRows [][]any) {
			fixture.RunStep(t, p.Ctx, b, recorder, fixture.Select("SELECT relname FROM pg_catalog.pg_class WHERE relname='idx'", []string{"relname"}, []string{"NAME"}, indexRows))
			fixture.RunStep(t, p.Ctx, b, recorder, fixture.Select("SELECT n FROM t ORDER BY n", []string{"n"}, []string{"INT4"}, nil))
			if fixture.SettingsPhysical(t, b) != identity {
				t.Fatal("index build changed physical owner")
			}
		}
		for _, tc := range []struct{ setting, code, message string }{
			{"lock_timeout", "55P03", "canceling statement due to lock timeout"},
			{"statement_timeout", "57014", "canceling statement due to statement timeout"},
		} {
			fixture.LockExec(t, p.Ctx, b, "SET "+tc.setting+"='20ms'", 0)
			step := fixture.ImplicitError(createSQL, fixture.Diagnostic{Code: tc.code, Message: tc.message})
			step.ErrorStage = "execute"
			fixture.RunStep(t, p.Ctx, held, recorder, step)
			resetSQL := "SET " + tc.setting + "=0"
			// Reset Execute is still timed and must succeed without retry.
			fixture.RunStep(t, p.Ctx, fixture.HeldStatement{Executor: b, Statement: resets[tc.setting], SQL: resetSQL}, recorder, fixture.Exec(resetSQL, 0))
			state(nil)
		}
		if err := tx.Rollback(); err != nil {
			t.Fatal(err)
		}
		state(nil)
		fixture.RunStep(t, p.Ctx, b, recorder, fixture.Exec(createSQL, 0))
		state([][]any{{"idx"}})
	})
}
