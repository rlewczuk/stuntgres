package reference_test

import "testing"

func TestReferenceTransactionIsolationQueuedConflictTimeout(t *testing.T) {
	for _, own := range []string{"UPDATE t SET v=30 WHERE v=10", "DELETE FROM t WHERE v=10"} {
		for _, prepared := range []bool{false, true} {
			t.Run(own+map[bool]string{false: "/direct", true: "/prepared"}[prepared], func(t *testing.T) {
				p := newReferenceScenario(t)
				for _, backend := range p.backends() {
					t.Run(backend.name, func(t *testing.T) {
						a, b := referenceLockConnections(t, p.ctx, backend.db)
						run := func(x referenceExecutor, s referenceStep) { referenceRunStep(t, p.ctx, x, backend.recorder, s) }
						run(b, referenceExec("CREATE TABLE t(v INT)", 0))
						run(b, referenceExec("INSERT INTO t VALUES(10)", 1))
						run(a, referenceExec("BEGIN ISOLATION LEVEL REPEATABLE READ", 0))
						run(a, referenceTransactionRows(false, "SELECT v FROM t", 10))
						run(b, referenceExec("BEGIN", 0))
						run(b, referenceExec("UPDATE t SET v=20", 1))
						run(b, referenceExec("DELETE FROM t", 1))
						run(a, referenceExec("SET statement_timeout='2ms'", 0))
						// The actual update-and-delete holder remains live throughout the
						// timer. Do not poll for an enqueue after a 2ms timeout has fired.
						out := relationOutcome{}
						if prepared {
							stmt, err := a.PrepareContext(p.ctx, own)
							if err != nil || stmt == nil {
								t.Fatal("queued mutation must prepare successfully", stmt, err)
							}
							out.result, out.err = stmt.ExecContext(p.ctx)
							if err := stmt.Close(); err != nil {
								t.Fatal(err)
							}
						} else {
							out.result, out.err = a.ExecContext(p.ctx, own)
						}
						if out.result != nil {
							t.Fatal("timed out mutation returned a result")
						}
						action := "updating"
						if own[0] == 'D' {
							action = "deleting"
						}
						referenceRowTimeoutDiagnostic(t, out.err, backend.pg, "57014", "canceling statement due to statement timeout", action)
						run(a, referenceTransactionError(false, "SELECT num_nonnulls(1)", referenceTransactionAborted()))
						run(a, referenceExec("ROLLBACK", 0))
						run(b, referenceExec("ROLLBACK", 0))
						run(b, referenceTransactionRows(false, "SELECT v FROM t", 10))
					})
				}
			})
		}
	}
}
