package reference_test

import (
	"database/sql/driver"
	"stuntgres/reference/fixture"
	"testing"
)

func TestReferenceSerializableWriteBeforeRead(t *testing.T) { serializableReferenceRetained(t, false) }
func TestReferenceSerializableRetention(t *testing.T)       { serializableReferenceRetained(t, true) }
func serializableReferenceRetained(t *testing.T, endSession bool) {
	serializableReferenceScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend) {
		a, c := fixture.LockConnections(t, p.Ctx, b.DB)
		run := func(x fixture.Executor, s fixture.Step) { serializableReferenceRun(t, p, b, x, s) }
		for _, table := range []string{"p", "q"} {
			run(a, fixture.Exec("CREATE TABLE "+table+"(v INT)", 0))
			run(a, fixture.Exec("INSERT INTO "+table+" VALUES(0)", 1))
		}
		run(c, fixture.Exec("BEGIN ISOLATION LEVEL SERIALIZABLE", 0))
		run(c, fixture.Select("SELECT num_nonnulls(1)", []string{"num_nonnulls"}, []string{"INT4"}, [][]any{{int64(1)}}))
		run(a, fixture.Exec("BEGIN ISOLATION LEVEL SERIALIZABLE", 0))
		run(a, fixture.TransactionRows(false, "SELECT v FROM p", 0))
		run(a, fixture.Exec("INSERT INTO q VALUES(1)", 1))
		run(a, fixture.Exec("COMMIT", 0))
		if endSession {
			if err := a.Raw(func(any) error { return driver.ErrBadConn }); err != driver.ErrBadConn {
				t.Fatal(err)
			}
			a.Close()
		}
		run(c, fixture.TransactionRows(false, "SELECT v FROM q", 0))
		err := serializableReferenceWrite(t, p.Ctx, c, "INSERT INTO p VALUES(1)", false, 1)
		if err == nil {
			_, err = c.ExecContext(p.Ctx, "COMMIT")
			if err == nil {
				t.Fatal("retained writer/predicate history lost")
			}
			serializableReferenceFailure(t, err)
		}
		run(c, fixture.Exec("ROLLBACK", 0))
		run(c, fixture.TransactionRows(false, "SELECT v FROM p", 0))
		run(c, fixture.TransactionRows(false, "SELECT v FROM q ORDER BY v", 0, 1))
		run(c, fixture.Exec("BEGIN ISOLATION LEVEL SERIALIZABLE", 0))
		run(c, fixture.Exec("INSERT INTO p VALUES(2)", 1))
		run(c, fixture.Exec("COMMIT", 0))
		run(c, fixture.TransactionRows(false, "SELECT v FROM p ORDER BY v", 0, 2))
	})
}

func TestReferenceSerializableAccessPaths(t *testing.T) {
	for _, path := range []string{"join", "recursive_view", "insert_select", "update_from", "delete"} {
		for _, held := range []bool{false, true} {
			t.Run(path+map[bool]string{false: "/direct", true: "/held"}[held], func(t *testing.T) {
				serializableReferenceScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend) {
					a, c := fixture.LockConnections(t, p.Ctx, b.DB)
					run := func(x fixture.Executor, s fixture.Step) { serializableReferenceRun(t, p, b, x, s) }
					for _, table := range []string{"p", "q", "sink", "aux"} {
						run(a, fixture.Exec("CREATE TABLE "+table+"(v INT)", 0))
					}
					run(a, fixture.Exec("INSERT INTO aux VALUES(9)", 1))
					if path == "recursive_view" {
						run(a, fixture.Exec("CREATE VIEW leaf AS SELECT v FROM p", 0))
						run(a, fixture.Exec("CREATE VIEW outer_view AS SELECT v FROM leaf", 0))
					}
					run(a, fixture.Exec("BEGIN ISOLATION LEVEL SERIALIZABLE", 0))
					run(c, fixture.Exec("BEGIN ISOLATION LEVEL SERIALIZABLE", 0))
					q := "SELECT p.v FROM p JOIN aux ON p.v=aux.v"
					s := fixture.TransactionRows(false, q)
					switch path {
					case "recursive_view":
						q = "SELECT v FROM outer_view"
						s = fixture.TransactionRows(false, q)
					case "insert_select":
						q = "INSERT INTO sink SELECT v FROM p"
						s = fixture.Exec(q, 0)
					case "update_from":
						q = "UPDATE p SET v=aux.v FROM aux WHERE p.v=aux.v"
						s = fixture.Exec(q, 0)
					case "delete":
						q = "DELETE FROM p WHERE v=1"
						s = fixture.Exec(q, 0)
					}
					run(serializableReferenceHeld(t, p.Ctx, a, q, held), s)
					run(c, fixture.TransactionRows(false, "SELECT v FROM q"))
					run(a, fixture.Exec("INSERT INTO q VALUES(1)", 1))
					run(c, fixture.Exec("INSERT INTO p VALUES(1)", 1))
					run(a, fixture.Exec("COMMIT", 0))
					want := serializableReferenceWant("Canceled on identification as a pivot, during commit attempt.")
					_, err := c.ExecContext(p.Ctx, "COMMIT")
					fixture.AssertDiagnostic(t, err, &want)
					run(a, fixture.TransactionRows(false, "SELECT v FROM p"))
					run(a, fixture.TransactionRows(false, "SELECT v FROM q", 1))
					run(a, fixture.TransactionRows(false, "SELECT v FROM sink"))
				})
			})
		}
	}
}

func TestReferenceSerializableSavepoints(t *testing.T) {
	for _, rollbackWrite := range []bool{false, true} {
		t.Run(map[bool]string{false: "read_survives", true: "doom_survives"}[rollbackWrite], func(t *testing.T) {
			serializableReferenceScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend) {
				a, c := fixture.LockConnections(t, p.Ctx, b.DB)
				run := func(x fixture.Executor, s fixture.Step) { serializableReferenceRun(t, p, b, x, s) }
				for _, table := range []string{"p", "q", "r"} {
					run(a, fixture.Exec("CREATE TABLE "+table+"(v INT)", 0))
					run(a, fixture.Exec("INSERT INTO "+table+" VALUES(0)", 1))
				}
				run(a, fixture.Exec("BEGIN ISOLATION LEVEL SERIALIZABLE", 0))
				run(c, fixture.Exec("BEGIN ISOLATION LEVEL SERIALIZABLE", 0))
				run(a, fixture.TransactionRows(false, "SELECT v FROM p", 0))
				run(c, fixture.Exec("SAVEPOINT s", 0))
				run(c, fixture.TransactionRows(false, "SELECT v FROM q", 0))
				if !rollbackWrite {
					run(c, fixture.Exec("ROLLBACK TO s", 0))
				}
				run(a, fixture.Exec("INSERT INTO q VALUES(1)", 1))
				run(c, fixture.Exec("INSERT INTO p VALUES(1)", 1))
				run(c, fixture.Exec("INSERT INTO r VALUES(7)", 1))
				run(a, fixture.Exec("COMMIT", 0))
				if rollbackWrite {
					run(c, fixture.Exec("ROLLBACK TO s", 0))
				}
				want := serializableReferenceWant("Canceled on identification as a pivot, during commit attempt.")
				_, err := c.ExecContext(p.Ctx, "COMMIT")
				fixture.AssertDiagnostic(t, err, &want)
				run(a, fixture.TransactionRows(false, "SELECT v FROM p", 0))
				run(a, fixture.TransactionRows(false, "SELECT v FROM q ORDER BY v", 0, 1))
				run(a, fixture.TransactionRows(false, "SELECT v FROM r", 0))
			})
		})
	}
}

func TestReferenceSerializableLifecycle(t *testing.T) {
	serializableReferenceScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend) {
		a, c := fixture.LockConnections(t, p.Ctx, b.DB)
		run := func(x fixture.Executor, s fixture.Step) { serializableReferenceRun(t, p, b, x, s) }
		run(a, fixture.Exec("CREATE TABLE p(v INT)", 0))
		run(a, fixture.Exec("INSERT INTO p VALUES(0)", 1))
		run(a, fixture.Exec("BEGIN ISOLATION LEVEL SERIALIZABLE READ ONLY DEFERRABLE", 0))
		run(a, fixture.TransactionRows(false, "SELECT v FROM p", 0))
		run(c, fixture.Exec("INSERT INTO p VALUES(1)", 1))
		run(a, fixture.Exec("COMMIT AND CHAIN", 0))
		referenceIsolationShow(t, a, "transaction_isolation", "serializable")
		referenceIsolationShow(t, a, "transaction_read_only", "on")
		referenceIsolationShow(t, a, "transaction_deferrable", "on")
		run(a, fixture.TransactionRows(false, "SELECT v FROM p ORDER BY v", 0, 1))
		run(a, fixture.Exec("ROLLBACK", 0))
		// Full rollback excludes predicates/writes; subsequent nonoverlapping owners
		// cannot inherit the old conflict or private addition.
		run(a, fixture.Exec("BEGIN ISOLATION LEVEL SERIALIZABLE", 0))
		run(a, fixture.TransactionRows(false, "SELECT v FROM p ORDER BY v", 0, 1))
		run(a, fixture.Exec("INSERT INTO p VALUES(2)", 1))
		run(a, fixture.Exec("ROLLBACK", 0))
		run(c, fixture.Exec("BEGIN ISOLATION LEVEL SERIALIZABLE", 0))
		run(c, fixture.Exec("INSERT INTO p VALUES(3)", 1))
		run(c, fixture.Exec("COMMIT", 0))
		run(c, fixture.TransactionRows(false, "SELECT v FROM p ORDER BY v", 0, 1, 3))
	})
}
