//go:build reference

package reference_test

import (
	"database/sql"
	"testing"
)

func TestReferenceAlterNullabilityLifecycle(t *testing.T) {
	for _, q := range []string{"ALTER TABLE t DROP COLUMN b, ALTER a SET NOT NULL", "ALTER TABLE t ALTER a DROP NOT NULL, DROP CONSTRAINT IF EXISTS missing"} {
		t.Run(q, func(t *testing.T) {
			p := newReferencePair(t)
			for _, backend := range []struct {
				name   string
				db     *sql.DB
				record *referenceRecorder
				pg     bool
			}{{"postgres", p.postgres, p.postgresRecord, true}, {"native", p.memory, p.memoryRecord, false}} {
				t.Run(backend.name, func(t *testing.T) {
					a, b := referenceLockConnections(t, p.ctx, backend.db)
					run := func(s referenceStep) { referenceRunStep(t, p.ctx, a, backend.record, s) }
					run(referenceExec("CREATE TABLE t(a INT,b INT)", 0))
					run(referenceExec("INSERT INTO t VALUES(7,9)", 1))
					referenceLockExec(t, p.ctx, b, "SET lock_timeout='150ms'", 0)
					holderPID := 0
					if backend.pg {
						holderPID = referenceLockPID(t, p.ctx, a)
					}
					holder, err := a.BeginTx(p.ctx, nil)
					if err != nil {
						t.Fatal(err)
					}
					defer holder.Rollback()
					referenceLockExec(t, p.ctx, holder, "LOCK TABLE t IN ACCESS EXCLUSIVE MODE", 0)
					_, err = b.ExecContext(p.ctx, q)
					want := referenceDiagnostic{Severity: "ERROR", SeverityUnlocalized: "ERROR", Code: "55P03", Message: "canceling statement due to lock timeout"}
					referenceAssertDiagnostic(t, err, &want)
					referenceLockExec(t, p.ctx, b, "RESET lock_timeout", 0)
					if backend.pg {
						pid := referenceLockPID(t, p.ctx, b)
						oid := referenceLockOID(t, p.ctx, "public.t")
						waiter, err := b.BeginTx(p.ctx, nil)
						if err != nil {
							t.Fatal(err)
						}
						defer waiter.Rollback()
						referenceLockExec(t, p.ctx, waiter, "SAVEPOINT recover", 0)
						done := make(chan relationOutcome, 1)
						go func() { result, err := waiter.ExecContext(p.ctx, q); done <- relationOutcome{result: result, err: err} }()
						referenceLockWaitBehind(t, p.ctx, pid, holderPID, oid, "AccessExclusiveLock")
						var canceled bool
						if err := referenceAdmin.QueryRowContext(p.ctx, "SELECT pg_cancel_backend($1)", pid).Scan(&canceled); err != nil || !canceled {
							t.Fatal(canceled, err)
						}
						outcome := referenceLockResult(t, done)
						want = referenceDiagnostic{Severity: "ERROR", SeverityUnlocalized: "ERROR", Code: "57014", Message: "canceling statement due to user request"}
						referenceAssertDiagnostic(t, outcome.err, &want)
						referenceRunStep(t, p.ctx, waiter, backend.record, referenceImplicitError("ALTER TABLE t ALTER a SET NOT NULL", referenceDiagnostic{Code: "25P02", Message: "current transaction is aborted, commands ignored until end of transaction block"}))
						referenceLockExec(t, p.ctx, waiter, "ROLLBACK TO recover", 0)
						waiter.Rollback()
					}
					if err = holder.Rollback(); err != nil {
						t.Fatal(err)
					}
					run(referenceSelect("SELECT * FROM t", []string{"a", "b"}, []string{"INT4", "INT4"}, [][]any{{int64(7), int64(9)}}))
					referenceRunStep(t, p.ctx, b, backend.record, referenceExec(q, 0))
				})
			}
		})
	}
}
