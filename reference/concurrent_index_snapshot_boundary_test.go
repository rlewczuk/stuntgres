package reference_test

import (
	"stuntgres"
	"testing"
)

func TestReferenceConcurrentIndexSnapshotsNewer(t *testing.T) {
	concurrentIndexScenario(t, func(t *testing.T, p *referencePair, b referenceBackend) {
		a, newer, c, builder := concurrentIndexConn(t, p, b), concurrentIndexConn(t, p, b), concurrentIndexConn(t, p, b), concurrentIndexConn(t, p, b)
		ap, bp := concurrentIndexPID(t, p, b, a), concurrentIndexPID(t, p, b, builder)
		concurrentIndexExec(t, p, b, c, "CREATE TABLE t(n INTEGER)", 0)
		concurrentIndexExec(t, p, b, c, "INSERT INTO t VALUES(1)", 1)
		concurrentIndexExec(t, p, b, c, "CREATE TABLE u(n INTEGER)", 0)
		concurrentIndexExec(t, p, b, c, "INSERT INTO u VALUES(8)", 1)
		concurrentIndexExec(t, p, b, a, "BEGIN ISOLATION LEVEL REPEATABLE READ", 0)
		concurrentIndexRows(t, p, b, a, "SELECT n FROM u ORDER BY n", 8)
		w := concurrentIndexStart(t, p, builder, "CREATE INDEX CONCURRENTLY ix ON t(n)", false)
		concurrentIndexWait(t, p, b, "ix", stuntgres.IndexState{Ready: true, Live: true}, bp, ap, "waiting for old snapshots")
		// Advance actual committed visibility after validation; newer has no endangered xmin.
		concurrentIndexExec(t, p, b, c, "INSERT INTO u VALUES(9)", 1)
		concurrentIndexExec(t, p, b, newer, "BEGIN ISOLATION LEVEL REPEATABLE READ", 0)
		concurrentIndexRows(t, p, b, newer, "SELECT n FROM u ORDER BY n", 8, 9)
		concurrentIndexExec(t, p, b, a, "COMMIT", 0)
		if err := w.join(t, p); err != nil {
			t.Fatal(err)
		}
		concurrentIndexWait(t, p, b, "ix", stuntgres.IndexState{Ready: true, Valid: true, Live: true}, 0, 0, "")
		concurrentIndexRows(t, p, b, newer, "SELECT n FROM u ORDER BY n", 8, 9)
		concurrentIndexExec(t, p, b, newer, "ROLLBACK", 0)
	})
}

func TestReferenceConcurrentIndexWritersCompatibleReaders(t *testing.T) {
	for _, rowShare := range []bool{false, true} {
		t.Run(fmtConcurrentCase(rowShare, false, false), func(t *testing.T) {
			concurrentIndexScenario(t, func(t *testing.T, p *referencePair, b referenceBackend) {
				a, c, builder := concurrentIndexConn(t, p, b), concurrentIndexConn(t, p, b), concurrentIndexConn(t, p, b)
				concurrentIndexExec(t, p, b, c, "CREATE TABLE t(n INTEGER)", 0)
				concurrentIndexExec(t, p, b, c, "INSERT INTO t VALUES(1)", 1)
				concurrentIndexExec(t, p, b, a, "BEGIN ISOLATION LEVEL READ COMMITTED", 0)
				concurrentIndexRows(t, p, b, a, "SELECT n FROM t ORDER BY n", 1)
				if rowShare {
					concurrentIndexExec(t, p, b, a, "LOCK TABLE t IN ROW SHARE MODE", 0)
				}
				w := concurrentIndexStart(t, p, builder, "CREATE INDEX CONCURRENTLY ix ON t(n)", true)
				if err := w.join(t, p); err != nil {
					t.Fatal(err)
				}
				concurrentIndexWait(t, p, b, "ix", stuntgres.IndexState{Ready: true, Valid: true, Live: true}, 0, 0, "")
				concurrentIndexExec(t, p, b, a, "ROLLBACK", 0)
			})
		})
	}
}
