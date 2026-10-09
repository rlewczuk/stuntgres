package reference_test

import (
	"context"
	"testing"
	"time"
)

func TestReferenceRowLockKeyControl(t *testing.T) {
	p := newReferenceScenario(t)
	for _, backend := range p.backends() {
		t.Run(backend.name, func(t *testing.T) {
			a, b := referenceLockConnections(t, p.ctx, backend.db)
			run := func(s referenceStep) { referenceRunStep(t, p.ctx, b, backend.recorder, s) }
			referenceRunStep(t, p.ctx, a, backend.recorder, referenceExec("CREATE TABLE t(id INT UNIQUE,v INT)", 0))
			referenceRunStep(t, p.ctx, a, backend.recorder, referenceExec("INSERT INTO t VALUES(1,10)", 1))
			var ap, bp int
			if backend.pg {
				ap, bp = referenceRowPIDs(t, p.ctx, a, b)
			}
			tx, err := a.BeginTx(p.ctx, nil)
			if err != nil {
				t.Fatal(err)
			}
			defer tx.Rollback()
			referenceRunStep(t, p.ctx, tx, backend.recorder, referenceSelect("SELECT v FROM t FOR KEY SHARE", []string{"v"}, []string{"INT4"}, [][]any{{int64(10)}}))
			run(referenceExec("UPDATE t SET v=20 WHERE id=1", 1))
			if !backend.pg {
				run(referenceExec("SET lock_timeout='75ms'", 0))
				run(referencePrepared(referenceImplicitError("UPDATE t SET id=2 WHERE id=1", referenceDiagnostic{Code: "55P03", Message: "canceling statement due to lock timeout"})))
				run(referenceExec("SET lock_timeout=0", 0))
			}
			ctx, cancel := context.WithTimeout(p.ctx, 5*time.Second)
			defer cancel()
			done := referenceRowAsync(t, ctx, b, "UPDATE t SET id=2 WHERE id=1", true, false)
			if backend.pg {
				referenceRowWait(t, ctx, bp, ap)
			}
			if err := tx.Rollback(); err != nil {
				t.Fatal(err)
			}
			outcome := referenceLockResult(t, done)
			if outcome.err != nil {
				t.Fatal(outcome.err)
			}
			referenceLockAffected(t, outcome.result, 1)
			run(referencePrepared(referenceSelect("SELECT v FROM t WHERE id=2 FOR UPDATE", []string{"v"}, []string{"INT4"}, [][]any{{int64(20)}})))
		})
	}
}
