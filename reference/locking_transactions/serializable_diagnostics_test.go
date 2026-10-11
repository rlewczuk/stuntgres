package reference_test

import (
	"stuntgres/reference/fixture"
	"testing"
)

func TestReferenceSerializableDoomSites(t *testing.T) {
	for _, site := range []string{"read", "write", "commit"} {
		for _, held := range []bool{false, true} {
			for _, goTx := range []bool{false, true} {
				t.Run(site+map[bool]string{false: "/direct", true: "/held"}[held]+map[bool]string{false: "/sql", true: "/go"}[goTx], func(t *testing.T) {
					serializableReferenceScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend) {
						a, c := fixture.LockConnections(t, p.Ctx, b.DB)
						run := func(x fixture.Executor, s fixture.Step) { serializableReferenceRun(t, p, b, x, s) }
						for _, table := range []string{"p", "q", "r"} {
							run(a, fixture.Exec("CREATE TABLE "+table+"(v INT)", 0))
							run(a, fixture.Exec("INSERT INTO "+table+" VALUES(0)", 1))
						}
						x, ca := serializableReferenceBegin(t, p.Ctx, a, "SERIALIZABLE", goTx)
						y, cb := serializableReferenceBegin(t, p.Ctx, c, "SERIALIZABLE", goTx)
						run(x, fixture.TransactionRows(false, "SELECT v FROM p", 0))
						run(y, fixture.TransactionRows(false, "SELECT v FROM q", 0))
						run(x, fixture.Exec("INSERT INTO q VALUES(1)", 1))
						run(y, fixture.Exec("INSERT INTO p VALUES(1)", 1))
						fixture.State(t, ca(), "")
						if site == "commit" {
							want := serializableReferenceWant("Canceled on identification as a pivot, during commit attempt.")
							fixture.AssertDiagnostic(t, cb(), &want)
						} else {
							q := "SELECT v FROM q"
							detail := "Canceled on identification as a pivot, during conflict out checking."
							if site == "write" {
								q = "INSERT INTO r VALUES(9)"
								detail = "Canceled on identification as a pivot, during conflict in checking."
							}
							s := fixture.ErrorDiagnostic(q, serializableReferenceWant(detail))
							s.SelectRows = site == "read"
							executor := y
							if held {
								stmt, err := y.PrepareContext(p.Ctx, q)
								if err != nil {
									t.Fatal(err)
								}
								defer stmt.Close()
								executor = fixture.HeldStatement{Executor: y, Statement: stmt, SQL: q}
							}
							run(executor, s)
							s = fixture.ErrorDiagnostic("SELECT v FROM r", fixture.TransactionAborted())
							s.SelectRows = true
							run(y, s)
							if goTx {
								tx, ok := y.(interface{ Rollback() error })
								if !ok {
									t.Fatal("missing Go transaction")
								}
								fixture.State(t, tx.Rollback(), "")
							} else {
								run(y, fixture.Exec("ROLLBACK", 0))
							}
						}
						for _, table := range []string{"p", "q", "r"} {
							values := []int64{0}
							if table == "q" {
								values = append(values, 1)
							}
							run(a, fixture.TransactionRows(false, "SELECT v FROM "+table+" ORDER BY v", values...))
						}
						// A failed commit completely ends the owner; a fresh transaction works.
						run(c, fixture.TransactionRows(false, "SELECT v FROM r", 0))
					})
				})
			}
		}
	}
}

func TestReferenceSerializableAtomicity(t *testing.T) {
	serializableReferenceScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend) {
		a, c := fixture.LockConnections(t, p.Ctx, b.DB)
		run := func(x fixture.Executor, s fixture.Step) { serializableReferenceRun(t, p, b, x, s) }
		for _, table := range []string{"p", "q", "r"} {
			run(a, fixture.Exec("CREATE TABLE "+table+"(v INT)", 0))
			run(a, fixture.Exec("INSERT INTO "+table+" VALUES(0)", 1))
		}
		x, ca := serializableReferenceBegin(t, p.Ctx, a, "SERIALIZABLE", false)
		y, cb := serializableReferenceBegin(t, p.Ctx, c, "SERIALIZABLE", false)
		run(x, fixture.TransactionRows(false, "SELECT v FROM p", 0))
		run(y, fixture.TransactionRows(false, "SELECT v FROM q", 0))
		run(y, fixture.Exec("INSERT INTO r VALUES(7),(8),(9)", 3))
		run(x, fixture.Exec("INSERT INTO q VALUES(1),(2)", 2))
		run(y, fixture.Exec("INSERT INTO p VALUES(1),(2)", 2))
		fixture.State(t, ca(), "")
		want := serializableReferenceWant("Canceled on identification as a pivot, during commit attempt.")
		fixture.AssertDiagnostic(t, cb(), &want)
		run(a, fixture.TransactionRows(false, "SELECT v FROM p", 0))
		run(a, fixture.TransactionRows(false, "SELECT v FROM q ORDER BY v", 0, 1, 2))
		run(a, fixture.TransactionRows(false, "SELECT v FROM r", 0))
	})
}

func TestReferenceSerializableNonblocking(t *testing.T) {
	serializableReferenceScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend) {
		a, c := fixture.LockConnections(t, p.Ctx, b.DB)
		run := func(x fixture.Executor, s fixture.Step) { serializableReferenceRun(t, p, b, x, s) }
		run(a, fixture.Exec("CREATE TABLE p(v INT)", 0))
		run(a, fixture.Exec("INSERT INTO p VALUES(0)", 1))
		run(a, fixture.Exec("BEGIN ISOLATION LEVEL SERIALIZABLE", 0))
		run(a, fixture.TransactionRows(false, "SELECT v FROM p", 0))
		run(c, fixture.Exec("BEGIN ISOLATION LEVEL SERIALIZABLE", 0))
		run(c, fixture.Exec("SET LOCAL statement_timeout='1s'", 0))
		run(c, fixture.Exec("INSERT INTO p VALUES(1)", 1))
		run(a, fixture.Exec("SET LOCAL statement_timeout='1s'", 0))
		run(a, fixture.TransactionRows(false, "SELECT v FROM p", 0))
		run(c, fixture.Exec("COMMIT", 0))
		run(a, fixture.Exec("COMMIT", 0))
		run(c, fixture.TransactionRows(false, "SELECT v FROM p ORDER BY v", 0, 1))
	})
}
