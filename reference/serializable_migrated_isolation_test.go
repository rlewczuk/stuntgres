package reference_test

import "testing"

// Complete assertion set from smoke/transaction_isolation_test.go, sharing
// public Conn routes and independent expectations with pinned PostgreSQL.
func TestReferenceSerializableMigratedIsolation(t *testing.T) {
	for _, name := range []string{"RC", "RU", "RR", "SnapshotPhases", "ReadOnlyDefaultsTiming", "Chain", "AbortedChain", "ConcurrentUpdateDirect", "ConcurrentUpdatePrepared", "ConflictUpdateThenDelete", "DeleteVisibilityAndReadOnly"} {
		t.Run(name, func(t *testing.T) {
			serializableReferenceScenario(t, func(t *testing.T, p *referencePair, b referenceBackend) {
				a, c := referenceLockConnections(t, p.ctx, b.db)
				exec := func(x referenceExecutor, q string, n int64) {
					serializableReferenceRun(t, p, b, x, referenceExec(q, n))
				}
				rows := func(x referenceExecutor, q string, want ...int64) {
					serializableReferenceRun(t, p, b, x, referenceTransactionRows(false, q, want...))
				}
				diagnostic := func(err error, code, message string) {
					want := controlsReferenceDiagnostic(code, message)
					referenceAssertDiagnostic(t, err, &want)
				}
				for _, table := range []string{"t", "u", "e"} {
					exec(c, "CREATE TABLE "+table+"(v INT)", 0)
				}
				exec(c, "INSERT INTO t VALUES(1)", 1)
				exec(c, "INSERT INTO u VALUES(10)", 1)
				switch name {
				case "RC", "RU", "RR":
					mode := map[string]string{"RC": "READ COMMITTED", "RU": "READ UNCOMMITTED", "RR": "REPEATABLE READ"}[name]
					exec(a, "BEGIN ISOLATION LEVEL "+mode, 0)
					exec(c, "INSERT INTO t VALUES(2)", 1)
					rows(a, "SELECT v FROM t ORDER BY v", 1, 2)
					exec(c, "BEGIN", 0)
					exec(c, "INSERT INTO t VALUES(3)", 1)
					exec(c, "UPDATE u SET v=20", 1)
					exec(c, "INSERT INTO e VALUES(30)", 1)
					exec(c, "COMMIT", 0)
					if name == "RR" {
						rows(a, "SELECT v FROM t ORDER BY v", 1, 2)
						rows(a, "SELECT v FROM u", 10)
						rows(a, "SELECT v FROM e")
					} else {
						rows(a, "SELECT v FROM t ORDER BY v", 1, 2, 3)
						rows(a, "SELECT v FROM u", 20)
						rows(a, "SELECT v FROM e", 30)
					}
					exec(a, "INSERT INTO t VALUES(4)", 1)
					if name == "RR" {
						rows(a, "SELECT v FROM t ORDER BY v", 1, 2, 4)
					} else {
						rows(a, "SELECT v FROM t ORDER BY v", 1, 2, 3, 4)
					}
					exec(a, "COMMIT", 0)
					rows(c, "SELECT v FROM t ORDER BY v", 1, 2, 3, 4)
				case "SnapshotPhases":
					exec(a, "BEGIN ISOLATION LEVEL REPEATABLE READ", 0)
					held, err := a.PrepareContext(p.ctx, "SELECT v FROM t")
					if err != nil {
						t.Fatal(err)
					}
					defer held.Close()
					exec(c, "UPDATE t SET v=2", 1)
					rows(referenceHeldStatement{referenceExecutor: a, statement: held, query: "SELECT v FROM t"}, "SELECT v FROM t", 1)
					_, err = a.ExecContext(p.ctx, "SET TRANSACTION ISOLATION LEVEL READ COMMITTED")
					diagnostic(err, "25001", "SET TRANSACTION ISOLATION LEVEL must be called before any query")
				case "ReadOnlyDefaultsTiming":
					exec(a, "SET SESSION CHARACTERISTICS AS TRANSACTION ISOLATION LEVEL REPEATABLE READ, READ ONLY", 0)
					exec(a, "BEGIN", 0)
					referenceIsolationShow(t, a, "transaction_isolation", "repeatable read")
					held, err := a.PrepareContext(p.ctx, "INSERT INTO t VALUES(2)")
					if err != nil {
						t.Fatal(err)
					}
					defer held.Close()
					result, err := held.ExecContext(p.ctx)
					if result != nil {
						t.Fatal("read-only write returned result")
					}
					diagnostic(err, "25006", "cannot execute INSERT in a read-only transaction")
					exec(a, "ROLLBACK", 0)
					rows(c, "SELECT v FROM t", 1)
				case "Chain":
					exec(a, "BEGIN ISOLATION LEVEL REPEATABLE READ READ ONLY", 0)
					rows(a, "SELECT v FROM t", 1)
					exec(c, "INSERT INTO t VALUES(2)", 1)
					exec(a, "SET SESSION CHARACTERISTICS AS TRANSACTION ISOLATION LEVEL READ COMMITTED, READ WRITE", 0)
					exec(a, "COMMIT AND CHAIN", 0)
					rows(a, "SELECT v FROM t ORDER BY v", 1, 2)
					referenceIsolationShow(t, a, "transaction_isolation", "repeatable read")
				case "AbortedChain":
					exec(a, "BEGIN ISOLATION LEVEL REPEATABLE READ READ ONLY", 0)
					rows(a, "SELECT v FROM t", 1)
					exec(a, "SET SESSION CHARACTERISTICS AS TRANSACTION ISOLATION LEVEL REPEATABLE READ READ ONLY", 0)
					_, err := a.ExecContext(p.ctx, "DELETE FROM t")
					diagnostic(err, "25006", "cannot execute DELETE in a read-only transaction")
					exec(a, "ROLLBACK AND CHAIN", 0)
					for setting, want := range map[string]string{"transaction_isolation": "read committed", "transaction_read_only": "off", "default_transaction_isolation": "read committed", "default_transaction_read_only": "off"} {
						referenceIsolationShow(t, a, setting, want)
					}
					exec(c, "INSERT INTO t VALUES(2)", 1)
					rows(a, "SELECT v FROM t ORDER BY v", 1, 2)
					exec(a, "INSERT INTO t VALUES(3)", 1)
					exec(a, "ROLLBACK", 0)
					rows(c, "SELECT v FROM t ORDER BY v", 1, 2)
				case "ConflictUpdateThenDelete", "ConcurrentUpdateDirect", "ConcurrentUpdatePrepared":
					exec(a, "BEGIN ISOLATION LEVEL REPEATABLE READ", 0)
					rows(a, "SELECT v FROM t", 1)
					exec(c, "UPDATE t SET v=2", 1)
					q := "UPDATE t SET v=3"
					if name == "ConflictUpdateThenDelete" {
						exec(c, "DELETE FROM t", 1)
						q = "DELETE FROM t"
					}
					x := serializableReferenceHeld(t, p.ctx, a, q, name != "ConcurrentUpdateDirect")
					result, err := x.ExecContext(p.ctx, q)
					if result != nil {
						t.Fatal("predecessor failure returned result")
					}
					diagnostic(err, "40001", "could not serialize access due to concurrent update")
					exec(a, "ROLLBACK", 0)
					if name == "ConflictUpdateThenDelete" {
						rows(c, "SELECT v FROM t")
					} else {
						rows(c, "SELECT v FROM t", 2)
					}
				case "DeleteVisibilityAndReadOnly":
					exec(a, "BEGIN ISOLATION LEVEL REPEATABLE READ", 0)
					rows(a, "SELECT v FROM t", 1)
					exec(c, "DELETE FROM u", 1)
					rows(a, "SELECT v FROM u", 10)
					exec(a, "SET TRANSACTION READ ONLY", 0)
					_, err := a.ExecContext(p.ctx, "DELETE FROM t")
					diagnostic(err, "25006", "cannot execute DELETE in a read-only transaction")
					exec(a, "ROLLBACK", 0)
					rows(c, "SELECT v FROM t", 1)
				}
			})
		})
	}
}
