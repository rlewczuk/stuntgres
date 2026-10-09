package reference_test

import "testing"

func TestReferenceSerializableDoomSites(t *testing.T) {
	for _, site := range []string{"read", "write", "commit"} {
		for _, held := range []bool{false, true} {
			for _, goTx := range []bool{false, true} {
				t.Run(site+map[bool]string{false: "/direct", true: "/held"}[held]+map[bool]string{false: "/sql", true: "/go"}[goTx], func(t *testing.T) {
					serializableReferenceScenario(t, func(t *testing.T, p *referencePair, b referenceBackend) {
						a, c := referenceLockConnections(t, p.ctx, b.db)
						run := func(x referenceExecutor, s referenceStep) { serializableReferenceRun(t, p, b, x, s) }
						for _, table := range []string{"p", "q", "r"} {
							run(a, referenceExec("CREATE TABLE "+table+"(v INT)", 0))
							run(a, referenceExec("INSERT INTO "+table+" VALUES(0)", 1))
						}
						x, ca := serializableReferenceBegin(t, p.ctx, a, "SERIALIZABLE", goTx)
						y, cb := serializableReferenceBegin(t, p.ctx, c, "SERIALIZABLE", goTx)
						run(x, referenceTransactionRows(false, "SELECT v FROM p", 0))
						run(y, referenceTransactionRows(false, "SELECT v FROM q", 0))
						run(x, referenceExec("INSERT INTO q VALUES(1)", 1))
						run(y, referenceExec("INSERT INTO p VALUES(1)", 1))
						referenceState(t, ca(), "")
						if site == "commit" {
							want := serializableReferenceWant("Canceled on identification as a pivot, during commit attempt.")
							referenceAssertDiagnostic(t, cb(), &want)
						} else {
							q := "SELECT v FROM q"
							detail := "Canceled on identification as a pivot, during conflict out checking."
							if site == "write" {
								q = "INSERT INTO r VALUES(9)"
								detail = "Canceled on identification as a pivot, during conflict in checking."
							}
							s := referenceErrorDiagnostic(q, serializableReferenceWant(detail))
							s.selectRows = site == "read"
							executor := y
							if held {
								stmt, err := y.PrepareContext(p.ctx, q)
								if err != nil {
									t.Fatal(err)
								}
								defer stmt.Close()
								executor = referenceHeldStatement{referenceExecutor: y, statement: stmt, query: q}
							}
							run(executor, s)
							s = referenceErrorDiagnostic("SELECT v FROM r", referenceTransactionAborted())
							s.selectRows = true
							run(y, s)
							if goTx {
								tx, ok := y.(interface{ Rollback() error })
								if !ok {
									t.Fatal("missing Go transaction")
								}
								referenceState(t, tx.Rollback(), "")
							} else {
								run(y, referenceExec("ROLLBACK", 0))
							}
						}
						for _, table := range []string{"p", "q", "r"} {
							values := []int64{0}
							if table == "q" {
								values = append(values, 1)
							}
							run(a, referenceTransactionRows(false, "SELECT v FROM "+table+" ORDER BY v", values...))
						}
						// A failed commit completely ends the owner; a fresh transaction works.
						run(c, referenceTransactionRows(false, "SELECT v FROM r", 0))
					})
				})
			}
		}
	}
}

func TestReferenceSerializableAtomicity(t *testing.T) {
	serializableReferenceScenario(t, func(t *testing.T, p *referencePair, b referenceBackend) {
		a, c := referenceLockConnections(t, p.ctx, b.db)
		run := func(x referenceExecutor, s referenceStep) { serializableReferenceRun(t, p, b, x, s) }
		for _, table := range []string{"p", "q", "r"} {
			run(a, referenceExec("CREATE TABLE "+table+"(v INT)", 0))
			run(a, referenceExec("INSERT INTO "+table+" VALUES(0)", 1))
		}
		x, ca := serializableReferenceBegin(t, p.ctx, a, "SERIALIZABLE", false)
		y, cb := serializableReferenceBegin(t, p.ctx, c, "SERIALIZABLE", false)
		run(x, referenceTransactionRows(false, "SELECT v FROM p", 0))
		run(y, referenceTransactionRows(false, "SELECT v FROM q", 0))
		run(y, referenceExec("INSERT INTO r VALUES(7),(8),(9)", 3))
		run(x, referenceExec("INSERT INTO q VALUES(1),(2)", 2))
		run(y, referenceExec("INSERT INTO p VALUES(1),(2)", 2))
		referenceState(t, ca(), "")
		want := serializableReferenceWant("Canceled on identification as a pivot, during commit attempt.")
		referenceAssertDiagnostic(t, cb(), &want)
		run(a, referenceTransactionRows(false, "SELECT v FROM p", 0))
		run(a, referenceTransactionRows(false, "SELECT v FROM q ORDER BY v", 0, 1, 2))
		run(a, referenceTransactionRows(false, "SELECT v FROM r", 0))
	})
}

func TestReferenceSerializableNonblocking(t *testing.T) {
	serializableReferenceScenario(t, func(t *testing.T, p *referencePair, b referenceBackend) {
		a, c := referenceLockConnections(t, p.ctx, b.db)
		run := func(x referenceExecutor, s referenceStep) { serializableReferenceRun(t, p, b, x, s) }
		run(a, referenceExec("CREATE TABLE p(v INT)", 0))
		run(a, referenceExec("INSERT INTO p VALUES(0)", 1))
		run(a, referenceExec("BEGIN ISOLATION LEVEL SERIALIZABLE", 0))
		run(a, referenceTransactionRows(false, "SELECT v FROM p", 0))
		run(c, referenceExec("BEGIN ISOLATION LEVEL SERIALIZABLE", 0))
		run(c, referenceExec("SET LOCAL statement_timeout='1s'", 0))
		run(c, referenceExec("INSERT INTO p VALUES(1)", 1))
		run(a, referenceExec("SET LOCAL statement_timeout='1s'", 0))
		run(a, referenceTransactionRows(false, "SELECT v FROM p", 0))
		run(c, referenceExec("COMMIT", 0))
		run(a, referenceExec("COMMIT", 0))
		run(c, referenceTransactionRows(false, "SELECT v FROM p ORDER BY v", 0, 1))
	})
}
