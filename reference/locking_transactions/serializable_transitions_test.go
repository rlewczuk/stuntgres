package reference_test

import (
	"stuntgres/reference/fixture"
	"testing"
)

func TestReferenceSerializableEnrollment(t *testing.T) {
	for _, serializable := range []bool{false, true} {
		t.Run(map[bool]string{false: "leave", true: "enter_leave_reenter"}[serializable], func(t *testing.T) {
			serializableReferenceScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend) {
				a, c := fixture.LockConnections(t, p.Ctx, b.DB)
				run := func(x fixture.Executor, s fixture.Step) { serializableReferenceRun(t, p, b, x, s) }
				for _, table := range []string{"p", "q"} {
					run(a, fixture.Exec("CREATE TABLE "+table+"(v INT)", 0))
					run(a, fixture.Exec("INSERT INTO "+table+" VALUES(0)", 1))
				}
				for _, x := range []fixture.Executor{a, c} {
					run(x, fixture.Exec("BEGIN ISOLATION LEVEL READ COMMITTED", 0))
					run(x, fixture.Exec("SET TRANSACTION ISOLATION LEVEL SERIALIZABLE", 0))
					run(x, fixture.Exec("SET TRANSACTION ISOLATION LEVEL READ COMMITTED", 0))
					if serializable {
						run(x, fixture.Exec("SET TRANSACTION ISOLATION LEVEL SERIALIZABLE", 0))
					}
				}
				run(a, fixture.TransactionRows(false, "SELECT v FROM p", 0))
				run(c, fixture.TransactionRows(false, "SELECT v FROM q", 0))
				run(a, fixture.Exec("INSERT INTO q VALUES(1)", 1))
				run(c, fixture.Exec("INSERT INTO p VALUES(1)", 1))
				run(a, fixture.Exec("COMMIT", 0))
				if serializable {
					want := serializableReferenceWant("Canceled on identification as a pivot, during commit attempt.")
					_, err := c.ExecContext(p.Ctx, "COMMIT")
					fixture.AssertDiagnostic(t, err, &want)
					run(a, fixture.TransactionRows(false, "SELECT v FROM p", 0))
				} else {
					run(c, fixture.Exec("COMMIT", 0))
					run(a, fixture.TransactionRows(false, "SELECT v FROM p ORDER BY v", 0, 1))
				}
				run(a, fixture.TransactionRows(false, "SELECT v FROM q ORDER BY v", 0, 1))
			})
		})
	}
}

func TestReferenceSerializableChangingRows(t *testing.T) {
	for _, kind := range []string{"update", "delete"} {
		for _, held := range []bool{false, true} {
			t.Run(kind+map[bool]string{false: "/direct", true: "/held"}[held], func(t *testing.T) {
				serializableReferenceScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend) {
					a, c := fixture.LockConnections(t, p.Ctx, b.DB)
					run := func(x fixture.Executor, s fixture.Step) { serializableReferenceRun(t, p, b, x, s) }
					for _, table := range []string{"p", "q"} {
						run(a, fixture.Exec("CREATE TABLE "+table+"(v INT)", 0))
						run(a, fixture.Exec("INSERT INTO "+table+" VALUES(0)", 1))
					}
					run(a, fixture.Exec("BEGIN ISOLATION LEVEL SERIALIZABLE", 0))
					run(c, fixture.Exec("BEGIN ISOLATION LEVEL SERIALIZABLE", 0))
					run(a, fixture.TransactionRows(false, "SELECT v FROM p WHERE v=0", 0))
					run(c, fixture.TransactionRows(false, "SELECT v FROM q WHERE v=0", 0))
					qa, qb := "UPDATE q SET v=1 WHERE v=0", "UPDATE p SET v=1 WHERE v=0"
					if kind == "delete" {
						qa, qb = "DELETE FROM q WHERE v=0", "DELETE FROM p WHERE v=0"
					}
					run(serializableReferenceHeld(t, p.Ctx, a, qa, held), fixture.Exec(qa, 1))
					run(serializableReferenceHeld(t, p.Ctx, c, qb, held), fixture.Exec(qb, 1))
					run(a, fixture.Exec("COMMIT", 0))
					want := serializableReferenceWant("Canceled on identification as a pivot, during commit attempt.")
					_, err := c.ExecContext(p.Ctx, "COMMIT")
					fixture.AssertDiagnostic(t, err, &want)
					run(a, fixture.TransactionRows(false, "SELECT v FROM p", 0))
					if kind == "update" {
						run(a, fixture.TransactionRows(false, "SELECT v FROM q", 1))
					} else {
						run(a, fixture.TransactionRows(false, "SELECT v FROM q"))
					}
				})
			})
		}
	}
}

func TestReferenceSerializableStickyWriteHistory(t *testing.T) {
	serializableReferenceScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend) {
		a, c, r := serializableReferenceWriters(t, p, b)
		run := func(x fixture.Executor, s fixture.Step) { serializableReferenceRun(t, p, b, x, s) }
		// T2 already has an outgoing edge to committed T1. Its participating write
		// is discarded, leaving an empty batch, but not restoring implicit RO status.
		run(c, fixture.Exec("SAVEPOINT s", 0))
		run(c, fixture.Exec("UPDATE x SET v=-11", 1))
		run(r, fixture.Exec("BEGIN ISOLATION LEVEL SERIALIZABLE READ ONLY", 0))
		run(r, fixture.TransactionRows(false, "SELECT v FROM x", 0))
		run(r, fixture.TransactionRows(false, "SELECT v FROM y", 20))
		run(r, fixture.Exec("COMMIT", 0))
		run(c, fixture.Exec("ROLLBACK TO s", 0))
		run(c, fixture.Exec("SET TRANSACTION READ ONLY", 0))
		want := serializableReferenceWant("Canceled on identification as a pivot, during commit attempt.")
		_, err := c.ExecContext(p.Ctx, "COMMIT")
		fixture.AssertDiagnostic(t, err, &want)
		run(a, fixture.TransactionRows(false, "SELECT v FROM x", 0))
		run(a, fixture.TransactionRows(false, "SELECT v FROM y", 20))
	})
}
