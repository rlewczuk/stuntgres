package reference_test

import (
	"strings"
	"stuntgres"
	"stuntgres/reference/fixture"
	"testing"
)

func TestReferenceSerializableNativeControls(t *testing.T) {
	for _, control := range []string{"restore", "reset", "failed_restore"} {
		t.Run(control, func(t *testing.T) {
			serializableReferenceScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend) {
				a, c := fixture.LockConnections(t, p.Ctx, b.DB)
				run := func(x fixture.Executor, s fixture.Step) { serializableReferenceRun(t, p, b, x, s) }
				run(a, fixture.Exec("CREATE TABLE t(v INT)", 0))
				run(a, fixture.Exec("INSERT INTO t VALUES(1)", 1))
				run(a, fixture.Exec("BEGIN ISOLATION LEVEL SERIALIZABLE", 0))
				run(a, fixture.Exec("INSERT INTO t VALUES(2)", 1))
				rows, err := a.QueryContext(p.Ctx, "SELECT v FROM t ORDER BY v")
				if err != nil {
					t.Fatal(err)
				}
				defer rows.Close()
				if !b.PG {
					switch control {
					case "restore":
						if err := stuntgres.Restore(p.Ctx, p.Native, strings.NewReader("CREATE TABLE t(v INT); INSERT INTO t VALUES(99);")); err != nil {
							t.Fatal(err)
						}
					case "reset":
						if err := stuntgres.Reset(p.Ctx, p.Native); err != nil {
							t.Fatal(err)
						}
					case "failed_restore":
						if err := stuntgres.Restore(p.Ctx, p.Native, strings.NewReader("CREATE TABLE t(v INT); INSERT INTO t VALUES('bad');")); err == nil {
							t.Fatal("invalid replacement succeeded")
						}
					}
				}
				fixture.LimitLockValues(t, fixture.RelationOutcome{Rows: rows}, b.PG, 1, 2)
				if b.PG || control == "failed_restore" {
					run(c, fixture.TransactionRows(false, "SELECT v FROM t", 1))
					run(a, fixture.Exec("COMMIT", 0))
					run(c, fixture.TransactionRows(false, "SELECT v FROM t ORDER BY v", 1, 2))
				} else {
					result, err := a.ExecContext(p.Ctx, "COMMIT")
					if result != nil {
						t.Fatal("stale transaction returned result")
					}
					fixture.TransactionOrdinary(t, err, "stuntgres: catalog replaced before publication")
					if control == "restore" {
						run(c, fixture.TransactionRows(false, "SELECT v FROM t", 99))
					} else {
						run(c, fixture.Exec("CREATE TABLE t(v INT)", 0))
						run(c, fixture.Exec("INSERT INTO t VALUES(99)", 1))
					}
					run(c, fixture.Exec("BEGIN ISOLATION LEVEL SERIALIZABLE", 0))
					run(c, fixture.Exec("INSERT INTO t VALUES(3)", 1))
					run(c, fixture.Exec("COMMIT", 0))
					run(c, fixture.TransactionRows(false, "SELECT v FROM t ORDER BY v", 3, 99))
				}
			})
		})
	}
}

func TestReferenceSerializablePredecessorConflicts(t *testing.T) {
	for _, peer := range []string{"UPDATE t SET v=2", "DELETE FROM t", "update_then_delete"} {
		for _, held := range []bool{false, true} {
			t.Run(peer+map[bool]string{false: "/direct", true: "/held"}[held], func(t *testing.T) {
				serializableReferenceScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend) {
					a, c := fixture.LockConnections(t, p.Ctx, b.DB)
					run := func(x fixture.Executor, s fixture.Step) { serializableReferenceRun(t, p, b, x, s) }
					run(a, fixture.Exec("CREATE TABLE t(v INT)", 0))
					run(a, fixture.Exec("INSERT INTO t VALUES(1)", 1))
					run(a, fixture.Exec("BEGIN ISOLATION LEVEL SERIALIZABLE", 0))
					run(a, fixture.TransactionRows(false, "SELECT v FROM t", 1))
					if peer == "update_then_delete" {
						run(c, fixture.Exec("UPDATE t SET v=2", 1))
						run(c, fixture.Exec("DELETE FROM t", 1))
					} else {
						run(c, fixture.Exec(peer, 1))
					}
					word := "update"
					if peer == "DELETE FROM t" {
						word = "delete"
					}
					q := "DELETE FROM t"
					run(serializableReferenceHeld(t, p.Ctx, a, q, held), fixture.ErrorDiagnostic(q, fixture.ControlsReferenceDiagnostic("40001", "could not serialize access due to concurrent "+word)))
					run(a, fixture.Exec("ROLLBACK", 0))
					if peer == "UPDATE t SET v=2" {
						run(c, fixture.TransactionRows(false, "SELECT v FROM t", 2))
					} else {
						run(c, fixture.TransactionRows(false, "SELECT v FROM t"))
					}
				})
			})
		}
	}
}
