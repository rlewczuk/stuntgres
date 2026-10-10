package reference_test

import (
	"stuntgres"
	"testing"
)

func TestReferenceConcurrentIndexWriters(t *testing.T) {
	for _, held := range []bool{false, true} {
		for _, commit := range []bool{false, true} {
			for _, lockOnly := range []bool{false, true} {
				t.Run(fmtConcurrentCase(held, commit, lockOnly), func(t *testing.T) {
					concurrentIndexScenario(t, func(t *testing.T, p *referencePair, b referenceBackend) {
						a, c := concurrentIndexConn(t, p, b), concurrentIndexConn(t, p, b)
						builder := concurrentIndexConn(t, p, b)
						ap, bp := concurrentIndexPID(t, p, b, a), concurrentIndexPID(t, p, b, builder)
						concurrentIndexExec(t, p, b, c, "CREATE TABLE t(n INTEGER)", 0)
						concurrentIndexExec(t, p, b, c, "INSERT INTO t VALUES(1)", 1)
						concurrentIndexExec(t, p, b, a, "BEGIN", 0)
						if lockOnly {
							concurrentIndexExec(t, p, b, a, "LOCK TABLE t IN ROW EXCLUSIVE MODE", 0)
						} else {
							concurrentIndexExec(t, p, b, a, "INSERT INTO t VALUES(2)", 1)
						}
						w := concurrentIndexStart(t, p, builder, "CREATE INDEX CONCURRENTLY ix ON t(n)", held)
						concurrentIndexWait(t, p, b, "ix", stuntgres.IndexState{Live: true}, bp, ap, "waiting for writers before build")
						concurrentIndexExec(t, p, b, c, "INSERT INTO t VALUES(3)", 1)
						concurrentIndexExec(t, p, b, c, "UPDATE t SET n=4 WHERE n=1", 1)
						concurrentIndexExec(t, p, b, c, "DELETE FROM t WHERE n=3", 1)
						end := "ROLLBACK"
						if commit {
							end = "COMMIT"
						}
						concurrentIndexExec(t, p, b, a, end, 0)
						if err := w.join(t, p); err != nil {
							t.Fatal(err)
						}
						concurrentIndexWait(t, p, b, "ix", stuntgres.IndexState{Ready: true, Valid: true, Live: true}, 0, 0, "")
						concurrentIndexExec(t, p, b, c, "INSERT INTO t VALUES(5)", 1)
						if commit && !lockOnly {
							concurrentIndexRows(t, p, b, c, "SELECT n FROM t ORDER BY n", 2, 4, 5)
						} else {
							concurrentIndexRows(t, p, b, c, "SELECT n FROM t ORDER BY n", 4, 5)
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
	concurrentIndexScenario(t, func(t *testing.T, p *referencePair, b referenceBackend) {
		a, d, c, builder := concurrentIndexConn(t, p, b), concurrentIndexConn(t, p, b), concurrentIndexConn(t, p, b), concurrentIndexConn(t, p, b)
		ap, dp, bp := concurrentIndexPID(t, p, b, a), concurrentIndexPID(t, p, b, d), concurrentIndexPID(t, p, b, builder)
		concurrentIndexExec(t, p, b, c, "CREATE TABLE t(n INTEGER)", 0)
		concurrentIndexExec(t, p, b, c, "INSERT INTO t VALUES(1)", 1)
		concurrentIndexExec(t, p, b, a, "BEGIN", 0)
		concurrentIndexExec(t, p, b, a, "INSERT INTO t VALUES(2)", 1)
		w := concurrentIndexStart(t, p, builder, "CREATE UNIQUE INDEX CONCURRENTLY ui ON t(n)", true)
		concurrentIndexWait(t, p, b, "ui", stuntgres.IndexState{Live: true}, bp, ap, "waiting for writers before build")
		concurrentIndexExec(t, p, b, d, "BEGIN", 0)
		concurrentIndexExec(t, p, b, d, "INSERT INTO t VALUES(1)", 1)
		concurrentIndexExec(t, p, b, a, "COMMIT", 0)
		concurrentIndexWait(t, p, b, "ui", stuntgres.IndexState{Ready: true, Live: true}, bp, dp, "waiting for writers before validation")
		want := concurrentIndexUnique("t", "ui", "Key (n)=(1) already exists.", false)
		concurrentIndexError(t, p, b, c, "INSERT INTO t VALUES(1)", want)
		concurrentIndexExec(t, p, b, d, "COMMIT", 0)
		referenceAssertDiagnostic(t, w.join(t, p), &want)
		concurrentIndexWait(t, p, b, "ui", stuntgres.IndexState{Ready: true, Live: true}, 0, 0, "")
		concurrentIndexRows(t, p, b, c, "SELECT n FROM t ORDER BY n", 1, 1, 2)
		concurrentIndexExec(t, p, b, c, "INSERT INTO t VALUES(3)", 1)
		concurrentIndexExec(t, p, b, c, "UPDATE t SET n=4 WHERE n=3", 1)
		concurrentIndexError(t, p, b, c, "UPDATE t SET n=1 WHERE n=4", want)
		concurrentIndexRows(t, p, b, c, "SELECT n FROM t ORDER BY n", 1, 1, 2, 4)
		concurrentIndexExec(t, p, b, c, "DROP INDEX ui", 0)
		concurrentIndexExec(t, p, b, c, "INSERT INTO t VALUES(1)", 1)
	})
}

func TestReferenceConcurrentIndexSnapshots(t *testing.T) {
	for _, unrelated := range []bool{false, true} {
		for _, commit := range []bool{false, true} {
			t.Run(fmtConcurrentCase(false, commit, unrelated), func(t *testing.T) {
				concurrentIndexScenario(t, func(t *testing.T, p *referencePair, b referenceBackend) {
					a, c, builder := concurrentIndexConn(t, p, b), concurrentIndexConn(t, p, b), concurrentIndexConn(t, p, b)
					ap, bp := concurrentIndexPID(t, p, b, a), concurrentIndexPID(t, p, b, builder)
					concurrentIndexExec(t, p, b, c, "CREATE TABLE t(n INTEGER)", 0)
					concurrentIndexExec(t, p, b, c, "INSERT INTO t VALUES(1)", 1)
					concurrentIndexExec(t, p, b, c, "CREATE TABLE u(n INTEGER)", 0)
					concurrentIndexExec(t, p, b, c, "INSERT INTO u VALUES(9)", 1)
					concurrentIndexExec(t, p, b, a, "BEGIN ISOLATION LEVEL REPEATABLE READ", 0)
					q := "SELECT n FROM t ORDER BY n"
					v := int64(1)
					if unrelated {
						q = "SELECT n FROM u ORDER BY n"
						v = 9
					}
					concurrentIndexRows(t, p, b, a, q, v)
					w := concurrentIndexStart(t, p, builder, "CREATE UNIQUE INDEX CONCURRENTLY ui ON t(n)", false)
					concurrentIndexWait(t, p, b, "ui", stuntgres.IndexState{Ready: true, Live: true}, bp, ap, "waiting for old snapshots")
					concurrentIndexExec(t, p, b, c, "INSERT INTO t VALUES(2)", 1)
					concurrentIndexError(t, p, b, c, "INSERT INTO t VALUES(1)", concurrentIndexUnique("t", "ui", "Key (n)=(1) already exists.", false))
					concurrentIndexRows(t, p, b, a, q, v)
					end := "ROLLBACK"
					if commit {
						end = "COMMIT"
					}
					concurrentIndexExec(t, p, b, a, end, 0)
					if err := w.join(t, p); err != nil {
						t.Fatal(err)
					}
					concurrentIndexWait(t, p, b, "ui", stuntgres.IndexState{Ready: true, Valid: true, Live: true}, 0, 0, "")
					concurrentIndexRows(t, p, b, c, "SELECT n FROM t ORDER BY n", 1, 2)
				})
			})
		}
	}
	for _, mode := range []string{"bare", "read-committed", "unrelated-writer"} {
		t.Run(mode, func(t *testing.T) {
			concurrentIndexScenario(t, func(t *testing.T, p *referencePair, b referenceBackend) {
				a, c, builder := concurrentIndexConn(t, p, b), concurrentIndexConn(t, p, b), concurrentIndexConn(t, p, b)
				concurrentIndexExec(t, p, b, c, "CREATE TABLE t(n INTEGER)", 0)
				concurrentIndexExec(t, p, b, c, "CREATE TABLE u(n INTEGER)", 0)
				concurrentIndexExec(t, p, b, a, "BEGIN ISOLATION LEVEL READ COMMITTED", 0)
				if mode == "read-committed" {
					concurrentIndexRows(t, p, b, a, "SELECT n FROM t ORDER BY n")
				}
				if mode == "unrelated-writer" {
					concurrentIndexExec(t, p, b, a, "INSERT INTO u VALUES(1)", 1)
				}
				w := concurrentIndexStart(t, p, builder, "CREATE INDEX CONCURRENTLY ix ON t(n)", false)
				if err := w.join(t, p); err != nil {
					t.Fatal(err)
				}
				concurrentIndexWait(t, p, b, "ix", stuntgres.IndexState{Ready: true, Valid: true, Live: true}, 0, 0, "")
				concurrentIndexExec(t, p, b, a, "ROLLBACK", 0)
			})
		})
	}
}
