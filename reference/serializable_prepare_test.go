package reference_test

import "testing"

func TestReferenceSerializablePrepareNoPredicate(t *testing.T) {
	serializableReferenceScenario(t, func(t *testing.T, p *referencePair, b referenceBackend) {
		a, c := referenceLockConnections(t, p.ctx, b.db)
		run := func(x referenceExecutor, s referenceStep) { serializableReferenceRun(t, p, b, x, s) }
		for _, table := range []string{"p", "q"} {
			run(a, referenceExec("CREATE TABLE "+table+"(v INT)", 0))
			run(a, referenceExec("INSERT INTO "+table+" VALUES(0)", 1))
		}
		run(a, referenceExec("BEGIN ISOLATION LEVEL SERIALIZABLE", 0))
		run(c, referenceExec("BEGIN ISOLATION LEVEL SERIALIZABLE", 0))
		held, err := a.PrepareContext(p.ctx, "SELECT v FROM p ORDER BY v")
		if err != nil {
			t.Fatal(err)
		}
		defer held.Close()
		// Describe can establish A's stable snapshot but cannot turn its unexecuted
		// p source into a predicate. B's q read creates only B -> A.
		run(c, referenceTransactionRows(false, "SELECT v FROM q", 0))
		run(a, referenceExec("INSERT INTO q VALUES(1)", 1))
		run(c, referenceExec("INSERT INTO p VALUES(1)", 1))
		run(a, referenceExec("COMMIT", 0))
		run(c, referenceExec("COMMIT", 0))
		run(a, referenceTransactionRows(false, "SELECT v FROM p ORDER BY v", 0, 1))
		run(a, referenceTransactionRows(false, "SELECT v FROM q ORDER BY v", 0, 1))
		// The held handle owns no execution snapshot after the old owner ends.
		run(referenceHeldStatement{referenceExecutor: a, statement: held, query: "SELECT v FROM p ORDER BY v"}, referenceTransactionRows(false, "SELECT v FROM p ORDER BY v", 0, 1))
	})
}
