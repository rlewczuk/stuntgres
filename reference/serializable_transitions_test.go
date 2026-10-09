package reference_test

import "testing"

func TestReferenceSerializableEnrollment(t *testing.T) {
	for _, serializable := range []bool{false, true} {
		t.Run(map[bool]string{false: "leave", true: "enter_leave_reenter"}[serializable], func(t *testing.T) {
			serializableReferenceScenario(t, func(t *testing.T, p *referencePair, b referenceBackend) {
				a, c := referenceLockConnections(t, p.ctx, b.db)
				run := func(x referenceExecutor, s referenceStep) { serializableReferenceRun(t, p, b, x, s) }
				for _, table := range []string{"p", "q"} {
					run(a, referenceExec("CREATE TABLE "+table+"(v INT)", 0))
					run(a, referenceExec("INSERT INTO "+table+" VALUES(0)", 1))
				}
				for _, x := range []referenceExecutor{a, c} {
					run(x, referenceExec("BEGIN ISOLATION LEVEL READ COMMITTED", 0))
					run(x, referenceExec("SET TRANSACTION ISOLATION LEVEL SERIALIZABLE", 0))
					run(x, referenceExec("SET TRANSACTION ISOLATION LEVEL READ COMMITTED", 0))
					if serializable {
						run(x, referenceExec("SET TRANSACTION ISOLATION LEVEL SERIALIZABLE", 0))
					}
				}
				run(a, referenceTransactionRows(false, "SELECT v FROM p", 0))
				run(c, referenceTransactionRows(false, "SELECT v FROM q", 0))
				run(a, referenceExec("INSERT INTO q VALUES(1)", 1))
				run(c, referenceExec("INSERT INTO p VALUES(1)", 1))
				run(a, referenceExec("COMMIT", 0))
				if serializable {
					want := serializableReferenceWant("Canceled on identification as a pivot, during commit attempt.")
					_, err := c.ExecContext(p.ctx, "COMMIT")
					referenceAssertDiagnostic(t, err, &want)
					run(a, referenceTransactionRows(false, "SELECT v FROM p", 0))
				} else {
					run(c, referenceExec("COMMIT", 0))
					run(a, referenceTransactionRows(false, "SELECT v FROM p ORDER BY v", 0, 1))
				}
				run(a, referenceTransactionRows(false, "SELECT v FROM q ORDER BY v", 0, 1))
			})
		})
	}
}

func TestReferenceSerializableChangingRows(t *testing.T) {
	for _, kind := range []string{"update", "delete"} {
		for _, held := range []bool{false, true} {
			t.Run(kind+map[bool]string{false: "/direct", true: "/held"}[held], func(t *testing.T) {
				serializableReferenceScenario(t, func(t *testing.T, p *referencePair, b referenceBackend) {
					a, c := referenceLockConnections(t, p.ctx, b.db)
					run := func(x referenceExecutor, s referenceStep) { serializableReferenceRun(t, p, b, x, s) }
					for _, table := range []string{"p", "q"} {
						run(a, referenceExec("CREATE TABLE "+table+"(v INT)", 0))
						run(a, referenceExec("INSERT INTO "+table+" VALUES(0)", 1))
					}
					run(a, referenceExec("BEGIN ISOLATION LEVEL SERIALIZABLE", 0))
					run(c, referenceExec("BEGIN ISOLATION LEVEL SERIALIZABLE", 0))
					run(a, referenceTransactionRows(false, "SELECT v FROM p WHERE v=0", 0))
					run(c, referenceTransactionRows(false, "SELECT v FROM q WHERE v=0", 0))
					qa, qb := "UPDATE q SET v=1 WHERE v=0", "UPDATE p SET v=1 WHERE v=0"
					if kind == "delete" {
						qa, qb = "DELETE FROM q WHERE v=0", "DELETE FROM p WHERE v=0"
					}
					run(serializableReferenceHeld(t, p.ctx, a, qa, held), referenceExec(qa, 1))
					run(serializableReferenceHeld(t, p.ctx, c, qb, held), referenceExec(qb, 1))
					run(a, referenceExec("COMMIT", 0))
					want := serializableReferenceWant("Canceled on identification as a pivot, during commit attempt.")
					_, err := c.ExecContext(p.ctx, "COMMIT")
					referenceAssertDiagnostic(t, err, &want)
					run(a, referenceTransactionRows(false, "SELECT v FROM p", 0))
					if kind == "update" {
						run(a, referenceTransactionRows(false, "SELECT v FROM q", 1))
					} else {
						run(a, referenceTransactionRows(false, "SELECT v FROM q"))
					}
				})
			})
		}
	}
}

func TestReferenceSerializableStickyWriteHistory(t *testing.T) {
	serializableReferenceScenario(t, func(t *testing.T, p *referencePair, b referenceBackend) {
		a, c, r := serializableReferenceWriters(t, p, b)
		run := func(x referenceExecutor, s referenceStep) { serializableReferenceRun(t, p, b, x, s) }
		// T2 already has an outgoing edge to committed T1. Its participating write
		// is discarded, leaving an empty batch, but not restoring implicit RO status.
		run(c, referenceExec("SAVEPOINT s", 0))
		run(c, referenceExec("UPDATE x SET v=-11", 1))
		run(r, referenceExec("BEGIN ISOLATION LEVEL SERIALIZABLE READ ONLY", 0))
		run(r, referenceTransactionRows(false, "SELECT v FROM x", 0))
		run(r, referenceTransactionRows(false, "SELECT v FROM y", 20))
		run(r, referenceExec("COMMIT", 0))
		run(c, referenceExec("ROLLBACK TO s", 0))
		run(c, referenceExec("SET TRANSACTION READ ONLY", 0))
		want := serializableReferenceWant("Canceled on identification as a pivot, during commit attempt.")
		_, err := c.ExecContext(p.ctx, "COMMIT")
		referenceAssertDiagnostic(t, err, &want)
		run(a, referenceTransactionRows(false, "SELECT v FROM x", 0))
		run(a, referenceTransactionRows(false, "SELECT v FROM y", 20))
	})
}
