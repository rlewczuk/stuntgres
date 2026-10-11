package reference_test

import (
	"stuntgres"
	"stuntgres/reference/fixture"
	"testing"
)

func TestReferenceConcurrentIndexWriters(t *testing.T) {
	for _, held := range []bool{false, true} {
		for _, commit := range []bool{false, true} {
			for _, lockOnly := range []bool{false, true} {
				t.Run(fmtConcurrentCase(held, commit, lockOnly), func(t *testing.T) {
					fixture.ConcurrentIndexScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend) {
						a, c := fixture.ConcurrentIndexConn(t, p, b), fixture.ConcurrentIndexConn(t, p, b)
						builder := fixture.ConcurrentIndexConn(t, p, b)
						ap, bp := fixture.ConcurrentIndexPID(t, p, b, a), fixture.ConcurrentIndexPID(t, p, b, builder)
						fixture.ConcurrentIndexExec(t, p, b, c, "CREATE TABLE t(n INTEGER)", 0)
						fixture.ConcurrentIndexExec(t, p, b, c, "INSERT INTO t VALUES(1)", 1)
						fixture.ConcurrentIndexExec(t, p, b, a, "BEGIN", 0)
						if lockOnly {
							fixture.ConcurrentIndexExec(t, p, b, a, "LOCK TABLE t IN ROW EXCLUSIVE MODE", 0)
						} else {
							fixture.ConcurrentIndexExec(t, p, b, a, "INSERT INTO t VALUES(2)", 1)
						}
						w := fixture.ConcurrentIndexStart(t, p, builder, "CREATE INDEX CONCURRENTLY ix ON t(n)", held)
						fixture.ConcurrentIndexWait(t, p, b, "ix", stuntgres.IndexState{Live: true}, bp, ap, "waiting for writers before build")
						fixture.ConcurrentIndexExec(t, p, b, c, "INSERT INTO t VALUES(3)", 1)
						fixture.ConcurrentIndexExec(t, p, b, c, "UPDATE t SET n=4 WHERE n=1", 1)
						fixture.ConcurrentIndexExec(t, p, b, c, "DELETE FROM t WHERE n=3", 1)
						end := "ROLLBACK"
						if commit {
							end = "COMMIT"
						}
						fixture.ConcurrentIndexExec(t, p, b, a, end, 0)
						if err := w.Join(t, p); err != nil {
							t.Fatal(err)
						}
						fixture.ConcurrentIndexWait(t, p, b, "ix", stuntgres.IndexState{Ready: true, Valid: true, Live: true}, 0, 0, "")
						fixture.ConcurrentIndexExec(t, p, b, c, "INSERT INTO t VALUES(5)", 1)
						if commit && !lockOnly {
							fixture.ConcurrentIndexRows(t, p, b, c, "SELECT n FROM t ORDER BY n", 2, 4, 5)
						} else {
							fixture.ConcurrentIndexRows(t, p, b, c, "SELECT n FROM t ORDER BY n", 4, 5)
						}
					})
				})
			}
		}
	}
}
func fmtConcurrentCase(held, commit, lock bool) string {
	s := "direct"
	if held {
		s = "held"
	}
	if commit {
		s += "/commit"
	} else {
		s += "/rollback"
	}
	if lock {
		s += "/lock-only"
	}
	return s
}

func TestReferenceConcurrentIndexSecondWriters(t *testing.T) {
	fixture.ConcurrentIndexScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend) {
		a, d, c, builder := fixture.ConcurrentIndexConn(t, p, b), fixture.ConcurrentIndexConn(t, p, b), fixture.ConcurrentIndexConn(t, p, b), fixture.ConcurrentIndexConn(t, p, b)
		ap, dp, bp := fixture.ConcurrentIndexPID(t, p, b, a), fixture.ConcurrentIndexPID(t, p, b, d), fixture.ConcurrentIndexPID(t, p, b, builder)
		fixture.ConcurrentIndexExec(t, p, b, c, "CREATE TABLE t(n INTEGER)", 0)
		fixture.ConcurrentIndexExec(t, p, b, c, "INSERT INTO t VALUES(1)", 1)
		fixture.ConcurrentIndexExec(t, p, b, a, "BEGIN", 0)
		fixture.ConcurrentIndexExec(t, p, b, a, "INSERT INTO t VALUES(2)", 1)
		w := fixture.ConcurrentIndexStart(t, p, builder, "CREATE UNIQUE INDEX CONCURRENTLY ui ON t(n)", true)
		fixture.ConcurrentIndexWait(t, p, b, "ui", stuntgres.IndexState{Live: true}, bp, ap, "waiting for writers before build")
		fixture.ConcurrentIndexExec(t, p, b, d, "BEGIN", 0)
		fixture.ConcurrentIndexExec(t, p, b, d, "INSERT INTO t VALUES(1)", 1)
		fixture.ConcurrentIndexExec(t, p, b, a, "COMMIT", 0)
		fixture.ConcurrentIndexWait(t, p, b, "ui", stuntgres.IndexState{Ready: true, Live: true}, bp, dp, "waiting for writers before validation")
		want := fixture.ConcurrentIndexUnique("t", "ui", "Key (n)=(1) already exists.", false)
		fixture.ConcurrentIndexError(t, p, b, c, "INSERT INTO t VALUES(1)", want)
		fixture.ConcurrentIndexExec(t, p, b, d, "COMMIT", 0)
		fixture.AssertDiagnostic(t, w.Join(t, p), &want)
		fixture.ConcurrentIndexWait(t, p, b, "ui", stuntgres.IndexState{Ready: true, Live: true}, 0, 0, "")
		fixture.ConcurrentIndexRows(t, p, b, c, "SELECT n FROM t ORDER BY n", 1, 1, 2)
		fixture.ConcurrentIndexExec(t, p, b, c, "INSERT INTO t VALUES(3)", 1)
		fixture.ConcurrentIndexExec(t, p, b, c, "UPDATE t SET n=4 WHERE n=3", 1)
		fixture.ConcurrentIndexError(t, p, b, c, "UPDATE t SET n=1 WHERE n=4", want)
		fixture.ConcurrentIndexRows(t, p, b, c, "SELECT n FROM t ORDER BY n", 1, 1, 2, 4)
		fixture.ConcurrentIndexExec(t, p, b, c, "DROP INDEX ui", 0)
		fixture.ConcurrentIndexExec(t, p, b, c, "INSERT INTO t VALUES(1)", 1)
	})
}

func TestReferenceConcurrentIndexSnapshots(t *testing.T) {
	for _, unrelated := range []bool{false, true} {
		for _, commit := range []bool{false, true} {
			t.Run(fmtConcurrentCase(false, commit, unrelated), func(t *testing.T) {
				fixture.ConcurrentIndexScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend) {
					a, c, builder := fixture.ConcurrentIndexConn(t, p, b), fixture.ConcurrentIndexConn(t, p, b), fixture.ConcurrentIndexConn(t, p, b)
					ap, bp := fixture.ConcurrentIndexPID(t, p, b, a), fixture.ConcurrentIndexPID(t, p, b, builder)
					fixture.ConcurrentIndexExec(t, p, b, c, "CREATE TABLE t(n INTEGER)", 0)
					fixture.ConcurrentIndexExec(t, p, b, c, "INSERT INTO t VALUES(1)", 1)
					fixture.ConcurrentIndexExec(t, p, b, c, "CREATE TABLE u(n INTEGER)", 0)
					fixture.ConcurrentIndexExec(t, p, b, c, "INSERT INTO u VALUES(9)", 1)
					fixture.ConcurrentIndexExec(t, p, b, a, "BEGIN ISOLATION LEVEL REPEATABLE READ", 0)
					q := "SELECT n FROM t ORDER BY n"
					v := int64(1)
					if unrelated {
						q = "SELECT n FROM u ORDER BY n"
						v = 9
					}
					fixture.ConcurrentIndexRows(t, p, b, a, q, v)
					w := fixture.ConcurrentIndexStart(t, p, builder, "CREATE UNIQUE INDEX CONCURRENTLY ui ON t(n)", false)
					fixture.ConcurrentIndexWait(t, p, b, "ui", stuntgres.IndexState{Ready: true, Live: true}, bp, ap, "waiting for old snapshots")
					fixture.ConcurrentIndexExec(t, p, b, c, "INSERT INTO t VALUES(2)", 1)
					fixture.ConcurrentIndexError(t, p, b, c, "INSERT INTO t VALUES(1)", fixture.ConcurrentIndexUnique("t", "ui", "Key (n)=(1) already exists.", false))
					fixture.ConcurrentIndexRows(t, p, b, a, q, v)
					end := "ROLLBACK"
					if commit {
						end = "COMMIT"
					}
					fixture.ConcurrentIndexExec(t, p, b, a, end, 0)
					if err := w.Join(t, p); err != nil {
						t.Fatal(err)
					}
					fixture.ConcurrentIndexWait(t, p, b, "ui", stuntgres.IndexState{Ready: true, Valid: true, Live: true}, 0, 0, "")
					fixture.ConcurrentIndexRows(t, p, b, c, "SELECT n FROM t ORDER BY n", 1, 2)
				})
			})
		}
	}
	for _, mode := range []string{"bare", "read-committed", "unrelated-writer"} {
		t.Run(mode, func(t *testing.T) {
			fixture.ConcurrentIndexScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend) {
				a, c, builder := fixture.ConcurrentIndexConn(t, p, b), fixture.ConcurrentIndexConn(t, p, b), fixture.ConcurrentIndexConn(t, p, b)
				fixture.ConcurrentIndexExec(t, p, b, c, "CREATE TABLE t(n INTEGER)", 0)
				fixture.ConcurrentIndexExec(t, p, b, c, "CREATE TABLE u(n INTEGER)", 0)
				fixture.ConcurrentIndexExec(t, p, b, a, "BEGIN ISOLATION LEVEL READ COMMITTED", 0)
				if mode == "read-committed" {
					fixture.ConcurrentIndexRows(t, p, b, a, "SELECT n FROM t ORDER BY n")
				}
				if mode == "unrelated-writer" {
					fixture.ConcurrentIndexExec(t, p, b, a, "INSERT INTO u VALUES(1)", 1)
				}
				w := fixture.ConcurrentIndexStart(t, p, builder, "CREATE INDEX CONCURRENTLY ix ON t(n)", false)
				if err := w.Join(t, p); err != nil {
					t.Fatal(err)
				}
				fixture.ConcurrentIndexWait(t, p, b, "ix", stuntgres.IndexState{Ready: true, Valid: true, Live: true}, 0, 0, "")
				fixture.ConcurrentIndexExec(t, p, b, a, "ROLLBACK", 0)
			})
		})
	}
}
