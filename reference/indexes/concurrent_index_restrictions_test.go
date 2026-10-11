package reference_test

import (
	"stuntgres"
	"stuntgres/reference/fixture"
	"testing"
)

func TestReferenceConcurrentIndexTransactionRestrictions(t *testing.T) {
	for _, held := range []bool{false, true} {
		for _, readOnly := range []bool{false, true} {
			for _, target := range []string{"t", "missing", "denied", "blocked"} {
				t.Run(fmtConcurrentCase(held, readOnly, false)+"/"+target, func(t *testing.T) {
					fixture.RoleReferenceCleanup(t, []string{"concurrent_reader"})
					fixture.ConcurrentIndexScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend) {
						c := fixture.ConcurrentIndexConn(t, p, b)
						fixture.ConcurrentIndexExec(t, p, b, c, "CREATE TABLE t(n INTEGER)", 0)
						actual := target
						if target == "denied" || target == "blocked" {
							actual = "t"
						}
						if target == "denied" {
							fixture.ConcurrentIndexExec(t, p, b, c, "CREATE USER concurrent_reader", 0)
							if b.PG {
								fixture.ConcurrentIndexExec(t, p, b, c, "GRANT USAGE ON SCHEMA public TO concurrent_reader", 0)
							}
							fixture.ConcurrentIndexExec(t, p, b, c, "SET ROLE concurrent_reader", 0)
							defer c.ExecContext(p.Ctx, "RESET ROLE")
						}
						var release func()
						if target == "blocked" {
							blocker := fixture.ConcurrentIndexConn(t, p, b)
							fixture.ConcurrentIndexExec(t, p, b, blocker, "BEGIN", 0)
							fixture.ConcurrentIndexExec(t, p, b, blocker, "LOCK TABLE t IN ACCESS EXCLUSIVE MODE", 0)
							release = func() { fixture.ConcurrentIndexExec(t, p, b, blocker, "ROLLBACK", 0) }
						}
						fixture.ConcurrentIndexExec(t, p, b, c, "SET lock_timeout='100ms'", 0)
						q := "CREATE INDEX CONCURRENTLY ix ON " + actual + "(n)"
						var execute fixture.Executor = c
						if held {
							stmt, err := c.PrepareContext(p.Ctx, q)
							if err != nil {
								t.Fatal(err)
							}
							defer stmt.Close()
							execute = fixture.HeldStatement{Executor: c, Statement: stmt, SQL: q}
							if _, found := fixture.ConcurrentIndexState(t, p, b, "ix"); found {
								t.Fatal("Prepare published index")
							}
						}
						begin := "BEGIN"
						if readOnly {
							begin = "BEGIN READ ONLY"
						}
						fixture.ConcurrentIndexExec(t, p, b, c, begin, 0)
						want := fixture.ControlsReferenceDiagnostic("25001", "CREATE INDEX CONCURRENTLY cannot run inside a transaction block")
						if readOnly {
							want = fixture.ControlsReferenceDiagnostic("25006", "cannot execute CREATE INDEX in a read-only transaction")
						}
						fixture.ConcurrentIndexError(t, p, b, execute, q, want)
						if _, found := fixture.ConcurrentIndexState(t, p, b, "ix"); found {
							t.Fatal("restricted index exists")
						}
						fixture.ConcurrentIndexError(t, p, b, c, "SELECT 1", fixture.ControlsReferenceDiagnostic("25P02", "current transaction is aborted, commands ignored until end of transaction block"))
						syntax := fixture.ControlsReferenceDiagnostic("42601", "syntax error at or near \"FORM\"")
						syntax.Position = "10"
						fixture.ConcurrentIndexError(t, p, b, c, "SELECT * FORM t", syntax)
						fixture.ConcurrentIndexExec(t, p, b, c, "ROLLBACK", 0)
						if release != nil {
							release()
						}
						if target == "denied" {
							fixture.ConcurrentIndexExec(t, p, b, c, "RESET ROLE", 0)
						}
						fixture.ConcurrentIndexExec(t, p, b, c, "CREATE INDEX CONCURRENTLY ix ON t(n)", 0)
					})
				})
			}
		}
	}
	fixture.ConcurrentIndexScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend) {
		c := fixture.ConcurrentIndexConn(t, p, b)
		fixture.ConcurrentIndexExec(t, p, b, c, "CREATE TABLE t(n INTEGER)", 0)
		fixture.ConcurrentIndexExec(t, p, b, c, "BEGIN", 0)
		if b.PG {
			fixture.ConcurrentIndexExec(t, p, b, c, "CREATE INDEX ix ON t(n)", 0)
		} else {
			fixture.ConcurrentIndexError(t, p, b, c, "CREATE INDEX ix ON t(n)", fixture.ControlsReferenceDiagnostic("0A000", "statement is unsupported in an explicit transaction"))
		}
		fixture.ConcurrentIndexExec(t, p, b, c, "ROLLBACK", 0)
		if _, found := fixture.ConcurrentIndexState(t, p, b, "ix"); found {
			t.Fatal("ordinary rollback retained index")
		}
		for _, held := range []bool{false, true} {
			query := "CREATE UNIQUE INDEX ui ON t(n) NULLS NOT DISTINCT"
			if held {
				stmt, err := c.PrepareContext(p.Ctx, query)
				if err != nil {
					t.Fatal(err)
				}
				result, err := stmt.ExecContext(p.Ctx)
				stmt.Close()
				if err != nil {
					t.Fatal(err)
				}
				count, err := result.RowsAffected()
				if err != nil || count != 0 {
					t.Fatal(count, err)
				}
			} else {
				fixture.ConcurrentIndexExec(t, p, b, c, query, 0)
			}
			fixture.ConcurrentIndexWait(t, p, b, "ui", stuntgres.IndexState{Ready: true, Valid: true, Live: true}, 0, 0, "")
			fixture.ConcurrentIndexExec(t, p, b, c, "INSERT INTO t VALUES(NULL)", 1)
			fixture.ConcurrentIndexError(t, p, b, c, "INSERT INTO t VALUES(NULL)", fixture.ConcurrentIndexUnique("t", "ui", "Key (n)=(null) already exists.", false))
			fixture.ConcurrentIndexExec(t, p, b, c, "DELETE FROM t", 1)
			fixture.ConcurrentIndexExec(t, p, b, c, "DROP INDEX ui", 0)
		}
	})
}

func TestReferenceConcurrentIndexOrdinaryBoundary(t *testing.T) {
	for _, held := range []bool{false, true} {
		for _, commit := range []bool{false, true} {
			t.Run(fmtConcurrentCase(held, commit, false), func(t *testing.T) {
				fixture.ConcurrentIndexScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend) {
					a, c, builder := fixture.ConcurrentIndexConn(t, p, b), fixture.ConcurrentIndexConn(t, p, b), fixture.ConcurrentIndexConn(t, p, b)
					ap, bp := fixture.ConcurrentIndexPID(t, p, b, a), fixture.ConcurrentIndexPID(t, p, b, builder)
					fixture.ConcurrentIndexExec(t, p, b, c, "CREATE TABLE t(n INTEGER)", 0)
					fixture.ConcurrentIndexExec(t, p, b, c, "INSERT INTO t VALUES(1)", 1)
					fixture.ConcurrentIndexExec(t, p, b, a, "BEGIN", 0)
					fixture.ConcurrentIndexExec(t, p, b, a, "INSERT INTO t VALUES(2)", 1)
					w := fixture.ConcurrentIndexStart(t, p, builder, "CREATE INDEX idx ON t(n)", held)
					if b.PG {
						fixture.LockWaitBehind(t, p.Ctx, bp, ap, fixture.LockOID(t, p.Ctx, "t"), "ShareLock")
						fixture.ConcurrentIndexExec(t, p, b, c, "SET lock_timeout='100ms'", 0)
						timeout := fixture.ControlsReferenceDiagnostic("55P03", "canceling statement due to lock timeout")
						timeout.Position = "13"
						fixture.ConcurrentIndexError(t, p, b, c, "INSERT INTO t VALUES(3)", timeout)
						fixture.ConcurrentIndexExec(t, p, b, c, "SET lock_timeout=0", 0)
					}
					// Native exact pending-Share/later-RX proof is internal U2: no public queue API.
					fixture.ConcurrentIndexRows(t, p, b, c, "SELECT n FROM t ORDER BY n", 1)
					end := "ROLLBACK"
					if commit {
						end = "COMMIT"
					}
					fixture.ConcurrentIndexExec(t, p, b, a, end, 0)
					if err := w.Join(t, p); err != nil {
						t.Fatal(err)
					}
					if commit {
						fixture.ConcurrentIndexRows(t, p, b, builder, "SELECT n FROM t ORDER BY n", 1, 2)
					} else {
						fixture.ConcurrentIndexRows(t, p, b, builder, "SELECT n FROM t ORDER BY n", 1)
					}
					fixture.ConcurrentIndexExec(t, p, b, c, "INSERT INTO t VALUES(3)", 1)
					if commit {
						fixture.ConcurrentIndexRows(t, p, b, c, "SELECT n FROM t ORDER BY n", 1, 2, 3)
					} else {
						fixture.ConcurrentIndexRows(t, p, b, c, "SELECT n FROM t ORDER BY n", 1, 3)
					}
				})
			})
		}
	}
}
