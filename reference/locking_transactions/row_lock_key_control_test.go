package reference_test

import (
	"context"
	"stuntgres/reference/fixture"
	"testing"
	"time"
)

func TestReferenceRowLockKeyControl(t *testing.T) {
	p := fixture.NewScenario(t)
	for _, backend := range p.Backends() {
		t.Run(backend.Name, func(t *testing.T) {
			a, b := fixture.LockConnections(t, p.Ctx, backend.DB)
			run := func(s fixture.Step) { fixture.RunStep(t, p.Ctx, b, backend.Recorder, s) }
			fixture.RunStep(t, p.Ctx, a, backend.Recorder, fixture.Exec("CREATE TABLE t(id INT UNIQUE,v INT)", 0))
			fixture.RunStep(t, p.Ctx, a, backend.Recorder, fixture.Exec("INSERT INTO t VALUES(1,10)", 1))
			var ap, bp int
			if backend.PG {
				ap, bp = fixture.RowPIDs(t, p.Ctx, a, b)
			}
			tx, err := a.BeginTx(p.Ctx, nil)
			if err != nil {
				t.Fatal(err)
			}
			defer tx.Rollback()
			fixture.RunStep(t, p.Ctx, tx, backend.Recorder, fixture.Select("SELECT v FROM t FOR KEY SHARE", []string{"v"}, []string{"INT4"}, [][]any{{int64(10)}}))
			run(fixture.Exec("UPDATE t SET v=20 WHERE id=1", 1))
			if !backend.PG {
				run(fixture.Exec("SET lock_timeout='75ms'", 0))
				run(fixture.Prepared(fixture.ImplicitError("UPDATE t SET id=2 WHERE id=1", fixture.Diagnostic{Code: "55P03", Message: "canceling statement due to lock timeout"})))
				run(fixture.Exec("SET lock_timeout=0", 0))
			}
			ctx, cancel := context.WithTimeout(p.Ctx, 5*time.Second)
			defer cancel()
			done := fixture.RowAsync(t, ctx, b, "UPDATE t SET id=2 WHERE id=1", true, false)
			if backend.PG {
				fixture.RowWait(t, ctx, bp, ap)
			}
			if err := tx.Rollback(); err != nil {
				t.Fatal(err)
			}
			outcome := fixture.LockResult(t, done)
			if outcome.Err != nil {
				t.Fatal(outcome.Err)
			}
			fixture.LockAffected(t, outcome.Result, 1)
			run(fixture.Prepared(fixture.Select("SELECT v FROM t WHERE id=2 FOR UPDATE", []string{"v"}, []string{"INT4"}, [][]any{{int64(20)}})))
		})
	}
}
