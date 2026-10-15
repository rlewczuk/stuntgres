package reference_test

import (
	"stuntgres/reference/fixture"
	"testing"
)

func TestReferenceTransactionIsolationQueuedConflictTimeout(t *testing.T) {
	for _, own := range []string{"UPDATE t SET v=30 WHERE v=10", "DELETE FROM t WHERE v=10"} {
		for _, prepared := range []bool{false, true} {
			t.Run(own+map[bool]string{false: "/direct", true: "/prepared"}[prepared], func(t *testing.T) {
				p := fixture.NewScenario(t)
				for _, backend := range p.Backends() {
					t.Run(backend.Name, func(t *testing.T) {
						a, b := fixture.LockConnections(t, p.Ctx, backend.DB)
						run := func(x fixture.Executor, s fixture.Step) { fixture.RunStep(t, p.Ctx, x, backend.Recorder, s) }
						run(b, fixture.Exec("CREATE TABLE t(v INT)", 0))
						run(b, fixture.Exec("INSERT INTO t VALUES(10)", 1))
						run(a, fixture.Exec("BEGIN ISOLATION LEVEL REPEATABLE READ", 0))
						run(a, fixture.TransactionRows(false, "SELECT v FROM t", 10))
						run(b, fixture.Exec("BEGIN", 0))
						run(b, fixture.Exec("UPDATE t SET v=20", 1))
						run(b, fixture.Exec("DELETE FROM t", 1))
						// The execution timer starts after any preparation, while the
						// actual update-and-delete holder remains live. Do not poll
						// for an enqueue after a 2ms timeout has fired.
						out := fixture.RelationOutcome{}
						if prepared {
							stmt, err := a.PrepareContext(p.Ctx, own)
							if err != nil || stmt == nil {
								t.Fatal("queued mutation must prepare successfully", stmt, err)
							}
							defer stmt.Close()
							run(a, fixture.Exec("SET statement_timeout='2ms'", 0))
							out.Result, out.Err = stmt.ExecContext(p.Ctx)
							if err := stmt.Close(); err != nil {
								t.Fatal(err)
							}
						} else {
							run(a, fixture.Exec("SET statement_timeout='2ms'", 0))
							out.Result, out.Err = a.ExecContext(p.Ctx, own)
						}
						if out.Result != nil {
							t.Fatal("timed out mutation returned a result")
						}
						action := "updating"
						if own[0] == 'D' {
							action = "deleting"
						}
						referenceRowTimeoutDiagnostic(t, out.Err, backend.PG, "57014", "canceling statement due to statement timeout", action)
						run(a, fixture.TransactionError(false, "SELECT num_nonnulls(1)", fixture.TransactionAborted()))
						run(a, fixture.Exec("ROLLBACK", 0))
						run(b, fixture.Exec("ROLLBACK", 0))
						run(b, fixture.TransactionRows(false, "SELECT v FROM t", 10))
					})
				}
			})
		}
	}
}
