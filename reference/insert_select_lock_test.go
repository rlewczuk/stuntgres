//go:build reference

package reference_test

import (
	"database/sql"
	"testing"
)

func TestReferenceInsertSelectSourceLock(t *testing.T) {
	p := newReferencePair(t)
	p.step(t, referenceExec("CREATE TABLE s(i INT)", 0))
	p.step(t, referenceExec("CREATE TABLE t(i INT)", 0))
	p.step(t, referenceExec("INSERT INTO s VALUES(1)", 1))
	for _, backend := range []struct {
		db     *sql.DB
		record *referenceRecorder
	}{{p.memory, p.memoryRecord}, {p.postgres, p.postgresRecord}} {
		waiter, err := backend.db.Conn(p.ctx)
		if err != nil {
			t.Fatal(err)
		}
		func() {
			defer waiter.Close()
			run := func(s referenceStep) { referenceRunStep(t, p.ctx, waiter, backend.record, s) }
			run(referenceExec("SET lock_timeout='40ms'", 0))
			defer waiter.ExecContext(p.ctx, "SET lock_timeout='0'")
			cached, err := waiter.PrepareContext(p.ctx, "INSERT INTO t SELECT i FROM s")
			if err != nil {
				t.Fatal(err)
			}
			defer cached.Close()
			holder, err := backend.db.BeginTx(p.ctx, nil)
			if err != nil {
				t.Fatal(err)
			}
			defer holder.Rollback()
			referenceRunStep(t, p.ctx, holder, backend.record, referenceExec("LOCK TABLE s IN ACCESS EXCLUSIVE MODE", 0))
			for _, prepared := range []bool{false, true} {
				failure := referenceImplicitError("INSERT INTO t(missing) SELECT i FROM s", referenceDiagnostic{Code: "42703", Message: `column "missing" of relation "t" does not exist`, Position: "15"})
				failure.prepared = prepared
				failure.errorStage = "execute"
				if prepared {
					failure.errorStage = "prepare"
				}
				run(failure)
			}
			// Holder acquisition is synchronous; every producer is blocked on source AS.
			for _, prepared := range []bool{false, true} {
				failure := referenceImplicitError("INSERT INTO t SELECT i FROM s", referenceDiagnostic{Code: "55P03", Message: "canceling statement due to lock timeout", Position: "29"})
				failure.prepared = prepared
				failure.errorStage = "execute"
				if prepared {
					failure.errorStage = "prepare"
				}
				run(failure)
			}
			result, err := cached.ExecContext(p.ctx)
			if result != nil {
				t.Fatal("failed cached producer exposed result")
			}
			referenceAssertDiagnostic(t, err, &referenceDiagnostic{Severity: "ERROR", SeverityUnlocalized: "ERROR", Code: "55P03", Message: "canceling statement due to lock timeout"})
			if err := holder.Rollback(); err != nil {
				t.Fatal(err)
			}
			run(referenceExec("INSERT INTO t SELECT i FROM s", 1))
			run(referenceSelect("SELECT i FROM t", []string{"i"}, []string{"INT4"}, [][]any{{int64(1)}}))
		}()
	}
}
