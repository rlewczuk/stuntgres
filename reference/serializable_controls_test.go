package reference_test

import (
	"strings"
	"testing"

	"stuntgres"
)

func TestReferenceSerializableNativeControls(t *testing.T) {
	for _, control := range []string{"restore", "reset", "failed_restore"} {
		t.Run(control, func(t *testing.T) {
			serializableReferenceScenario(t, func(t *testing.T, p *referencePair, b referenceBackend) {
				a, c := referenceLockConnections(t, p.ctx, b.db)
				run := func(x referenceExecutor, s referenceStep) { serializableReferenceRun(t, p, b, x, s) }
				run(a, referenceExec("CREATE TABLE t(v INT)", 0))
				run(a, referenceExec("INSERT INTO t VALUES(1)", 1))
				run(a, referenceExec("BEGIN ISOLATION LEVEL SERIALIZABLE", 0))
				run(a, referenceExec("INSERT INTO t VALUES(2)", 1))
				rows, err := a.QueryContext(p.ctx, "SELECT v FROM t ORDER BY v")
				if err != nil {
					t.Fatal(err)
				}
				defer rows.Close()
				if !b.pg {
					switch control {
					case "restore":
						if err := stuntgres.Restore(p.ctx, p.native, strings.NewReader("CREATE TABLE t(v INT); INSERT INTO t VALUES(99);")); err != nil {
							t.Fatal(err)
						}
					case "reset":
						if err := stuntgres.Reset(p.ctx, p.native); err != nil {
							t.Fatal(err)
						}
					case "failed_restore":
						if err := stuntgres.Restore(p.ctx, p.native, strings.NewReader("CREATE TABLE t(v INT); INSERT INTO t VALUES('bad');")); err == nil {
							t.Fatal("invalid replacement succeeded")
						}
					}
				}
				referenceLimitLockValues(t, relationOutcome{rows: rows}, b.pg, 1, 2)
				if b.pg || control == "failed_restore" {
					run(c, referenceTransactionRows(false, "SELECT v FROM t", 1))
					run(a, referenceExec("COMMIT", 0))
					run(c, referenceTransactionRows(false, "SELECT v FROM t ORDER BY v", 1, 2))
				} else {
					result, err := a.ExecContext(p.ctx, "COMMIT")
					if result != nil {
						t.Fatal("stale transaction returned result")
					}
					transactionOrdinary(t, err, "stuntgres: catalog replaced before publication")
					if control == "restore" {
						run(c, referenceTransactionRows(false, "SELECT v FROM t", 99))
					} else {
						run(c, referenceExec("CREATE TABLE t(v INT)", 0))
						run(c, referenceExec("INSERT INTO t VALUES(99)", 1))
					}
					run(c, referenceExec("BEGIN ISOLATION LEVEL SERIALIZABLE", 0))
					run(c, referenceExec("INSERT INTO t VALUES(3)", 1))
					run(c, referenceExec("COMMIT", 0))
					run(c, referenceTransactionRows(false, "SELECT v FROM t ORDER BY v", 3, 99))
				}
			})
		})
	}
}

func TestReferenceSerializablePredecessorConflicts(t *testing.T) {
	for _, peer := range []string{"UPDATE t SET v=2", "DELETE FROM t", "update_then_delete"} {
		for _, held := range []bool{false, true} {
			t.Run(peer+map[bool]string{false: "/direct", true: "/held"}[held], func(t *testing.T) {
				serializableReferenceScenario(t, func(t *testing.T, p *referencePair, b referenceBackend) {
					a, c := referenceLockConnections(t, p.ctx, b.db)
					run := func(x referenceExecutor, s referenceStep) { serializableReferenceRun(t, p, b, x, s) }
					run(a, referenceExec("CREATE TABLE t(v INT)", 0))
					run(a, referenceExec("INSERT INTO t VALUES(1)", 1))
					run(a, referenceExec("BEGIN ISOLATION LEVEL SERIALIZABLE", 0))
					run(a, referenceTransactionRows(false, "SELECT v FROM t", 1))
					if peer == "update_then_delete" {
						run(c, referenceExec("UPDATE t SET v=2", 1))
						run(c, referenceExec("DELETE FROM t", 1))
					} else {
						run(c, referenceExec(peer, 1))
					}
					word := "update"
					if peer == "DELETE FROM t" {
						word = "delete"
					}
					q := "DELETE FROM t"
					run(serializableReferenceHeld(t, p.ctx, a, q, held), referenceErrorDiagnostic(q, controlsReferenceDiagnostic("40001", "could not serialize access due to concurrent "+word)))
					run(a, referenceExec("ROLLBACK", 0))
					if peer == "UPDATE t SET v=2" {
						run(c, referenceTransactionRows(false, "SELECT v FROM t", 2))
					} else {
						run(c, referenceTransactionRows(false, "SELECT v FROM t"))
					}
				})
			})
		}
	}
}
