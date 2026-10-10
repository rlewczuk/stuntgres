package reference_test

import (
	"testing"

	"stuntgres"
)

func TestReferenceConcurrentIndexTransactionRestrictions(t *testing.T) {
	for _, held := range []bool{false, true} {
		for _, readOnly := range []bool{false, true} {
			for _, target := range []string{"t", "missing", "denied", "blocked"} {
				t.Run(fmtConcurrentCase(held, readOnly, false)+"/"+target, func(t *testing.T) {
					roleReferenceCleanup(t, []string{"concurrent_reader"})
					concurrentIndexScenario(t, func(t *testing.T, p *referencePair, b referenceBackend) {
						c := concurrentIndexConn(t, p, b)
						concurrentIndexExec(t, p, b, c, "CREATE TABLE t(n INTEGER)", 0)
						actual := target
						if target == "denied" || target == "blocked" {
							actual = "t"
						}
						if target == "denied" {
							concurrentIndexExec(t, p, b, c, "CREATE USER concurrent_reader", 0)
							if b.pg {
								concurrentIndexExec(t, p, b, c, "GRANT USAGE ON SCHEMA public TO concurrent_reader", 0)
							}
							concurrentIndexExec(t, p, b, c, "SET ROLE concurrent_reader", 0)
							defer c.ExecContext(p.ctx, "RESET ROLE")
						}
						var release func()
						if target == "blocked" {
							blocker := concurrentIndexConn(t, p, b)
							concurrentIndexExec(t, p, b, blocker, "BEGIN", 0)
							concurrentIndexExec(t, p, b, blocker, "LOCK TABLE t IN ACCESS EXCLUSIVE MODE", 0)
							release = func() { concurrentIndexExec(t, p, b, blocker, "ROLLBACK", 0) }
						}
						concurrentIndexExec(t, p, b, c, "SET lock_timeout='100ms'", 0)
						q := "CREATE INDEX CONCURRENTLY ix ON " + actual + "(n)"
						var execute referenceExecutor = c
						if held {
							stmt, err := c.PrepareContext(p.ctx, q)
							if err != nil {
								t.Fatal(err)
							}
							defer stmt.Close()
							execute = referenceHeldStatement{referenceExecutor: c, statement: stmt, query: q}
							if _, found := concurrentIndexState(t, p, b, "ix"); found {
								t.Fatal("Prepare published index")
							}
						}
						begin := "BEGIN"
						if readOnly {
							begin = "BEGIN READ ONLY"
						}
						concurrentIndexExec(t, p, b, c, begin, 0)
						want := controlsReferenceDiagnostic("25001", "CREATE INDEX CONCURRENTLY cannot run inside a transaction block")
						if readOnly {
							want = controlsReferenceDiagnostic("25006", "cannot execute CREATE INDEX in a read-only transaction")
						}
						concurrentIndexError(t, p, b, execute, q, want)
						if _, found := concurrentIndexState(t, p, b, "ix"); found {
							t.Fatal("restricted index exists")
						}
						concurrentIndexError(t, p, b, c, "SELECT 1", controlsReferenceDiagnostic("25P02", "current transaction is aborted, commands ignored until end of transaction block"))
						syntax := controlsReferenceDiagnostic("42601", "syntax error at or near \"FORM\"")
						syntax.Position = "10"
						concurrentIndexError(t, p, b, c, "SELECT * FORM t", syntax)
						concurrentIndexExec(t, p, b, c, "ROLLBACK", 0)
						if release != nil {
							release()
						}
						if target == "denied" {
							concurrentIndexExec(t, p, b, c, "RESET ROLE", 0)
						}
						concurrentIndexExec(t, p, b, c, "CREATE INDEX CONCURRENTLY ix ON t(n)", 0)
					})
				})
			}
		}
	}
	concurrentIndexScenario(t, func(t *testing.T, p *referencePair, b referenceBackend) {
		c := concurrentIndexConn(t, p, b)
		concurrentIndexExec(t, p, b, c, "CREATE TABLE t(n INTEGER)", 0)
		concurrentIndexExec(t, p, b, c, "BEGIN", 0)
		if b.pg {
			concurrentIndexExec(t, p, b, c, "CREATE INDEX ix ON t(n)", 0)
		} else {
			concurrentIndexError(t, p, b, c, "CREATE INDEX ix ON t(n)", controlsReferenceDiagnostic("0A000", "statement is unsupported in an explicit transaction"))
		}
		concurrentIndexExec(t, p, b, c, "ROLLBACK", 0)
		if _, found := concurrentIndexState(t, p, b, "ix"); found {
			t.Fatal("ordinary rollback retained index")
		}
		for _, held := range []bool{false, true} {
			query := "CREATE UNIQUE INDEX ui ON t(n) NULLS NOT DISTINCT"
			if held {
				stmt, err := c.PrepareContext(p.ctx, query)
				if err != nil {
					t.Fatal(err)
				}
				result, err := stmt.ExecContext(p.ctx)
				stmt.Close()
				if err != nil {
					t.Fatal(err)
				}
				count, err := result.RowsAffected()
				if err != nil || count != 0 {
					t.Fatal(count, err)
				}
			} else {
				concurrentIndexExec(t, p, b, c, query, 0)
			}
			concurrentIndexWait(t, p, b, "ui", stuntgres.IndexState{Ready: true, Valid: true, Live: true}, 0, 0, "")
			concurrentIndexExec(t, p, b, c, "INSERT INTO t VALUES(NULL)", 1)
			concurrentIndexError(t, p, b, c, "INSERT INTO t VALUES(NULL)", concurrentIndexUnique("t", "ui", "Key (n)=(null) already exists.", false))
			concurrentIndexExec(t, p, b, c, "DELETE FROM t", 1)
			concurrentIndexExec(t, p, b, c, "DROP INDEX ui", 0)
		}
	})
}

func TestReferenceConcurrentIndexOrdinaryBoundary(t *testing.T) {
	for _, held := range []bool{false, true} {
		for _, commit := range []bool{false, true} {
			t.Run(fmtConcurrentCase(held, commit, false), func(t *testing.T) {
				concurrentIndexScenario(t, func(t *testing.T, p *referencePair, b referenceBackend) {
					a, c, builder := concurrentIndexConn(t, p, b), concurrentIndexConn(t, p, b), concurrentIndexConn(t, p, b)
					ap, bp := concurrentIndexPID(t, p, b, a), concurrentIndexPID(t, p, b, builder)
					concurrentIndexExec(t, p, b, c, "CREATE TABLE t(n INTEGER)", 0)
					concurrentIndexExec(t, p, b, c, "INSERT INTO t VALUES(1)", 1)
					concurrentIndexExec(t, p, b, a, "BEGIN", 0)
					concurrentIndexExec(t, p, b, a, "INSERT INTO t VALUES(2)", 1)
					w := concurrentIndexStart(t, p, builder, "CREATE INDEX idx ON t(n)", held)
					if b.pg {
						referenceLockWaitBehind(t, p.ctx, bp, ap, referenceLockOID(t, p.ctx, "t"), "ShareLock")
						concurrentIndexExec(t, p, b, c, "SET lock_timeout='100ms'", 0)
						timeout := controlsReferenceDiagnostic("55P03", "canceling statement due to lock timeout")
						timeout.Position = "13"
						concurrentIndexError(t, p, b, c, "INSERT INTO t VALUES(3)", timeout)
						concurrentIndexExec(t, p, b, c, "SET lock_timeout=0", 0)
					}
					// Native exact pending-Share/later-RX proof is internal U2: no public queue API.
					concurrentIndexRows(t, p, b, c, "SELECT n FROM t ORDER BY n", 1)
					end := "ROLLBACK"
					if commit {
						end = "COMMIT"
					}
					concurrentIndexExec(t, p, b, a, end, 0)
					if err := w.join(t, p); err != nil {
						t.Fatal(err)
					}
					if commit {
						concurrentIndexRows(t, p, b, builder, "SELECT n FROM t ORDER BY n", 1, 2)
					} else {
						concurrentIndexRows(t, p, b, builder, "SELECT n FROM t ORDER BY n", 1)
					}
					concurrentIndexExec(t, p, b, c, "INSERT INTO t VALUES(3)", 1)
					if commit {
						concurrentIndexRows(t, p, b, c, "SELECT n FROM t ORDER BY n", 1, 2, 3)
					} else {
						concurrentIndexRows(t, p, b, c, "SELECT n FROM t ORDER BY n", 1, 3)
					}
				})
			})
		}
	}
}
