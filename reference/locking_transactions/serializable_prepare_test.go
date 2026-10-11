package reference_test

import (
	"stuntgres/reference/fixture"
	"testing"
)

func TestReferenceSerializablePrepareNoPredicate(t *testing.T) {
	serializableReferenceScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend) {
		a, c := fixture.LockConnections(t, p.Ctx, b.DB)
		run := func(x fixture.Executor, s fixture.Step) { serializableReferenceRun(t, p, b, x, s) }
		for _, table := range []string{"p", "q"} {
			run(a, fixture.Exec("CREATE TABLE "+table+"(v INT)", 0))
			run(a, fixture.Exec("INSERT INTO "+table+" VALUES(0)", 1))
		}
		run(a, fixture.Exec("BEGIN ISOLATION LEVEL SERIALIZABLE", 0))
		run(c, fixture.Exec("BEGIN ISOLATION LEVEL SERIALIZABLE", 0))
		held, err := a.PrepareContext(p.Ctx, "SELECT v FROM p ORDER BY v")
		if err != nil {
			t.Fatal(err)
		}
		defer held.Close()
		// Describe can establish A's stable snapshot but cannot turn its unexecuted
		// p source into a predicate. B's q read creates only B -> A.
		run(c, fixture.TransactionRows(false, "SELECT v FROM q", 0))
		run(a, fixture.Exec("INSERT INTO q VALUES(1)", 1))
		run(c, fixture.Exec("INSERT INTO p VALUES(1)", 1))
		run(a, fixture.Exec("COMMIT", 0))
		run(c, fixture.Exec("COMMIT", 0))
		run(a, fixture.TransactionRows(false, "SELECT v FROM p ORDER BY v", 0, 1))
		run(a, fixture.TransactionRows(false, "SELECT v FROM q ORDER BY v", 0, 1))
		// The held handle owns no execution snapshot after the old owner ends.
		run(fixture.HeldStatement{Executor: a, Statement: held, SQL: "SELECT v FROM p ORDER BY v"}, fixture.TransactionRows(false, "SELECT v FROM p ORDER BY v", 0, 1))
	})
}
