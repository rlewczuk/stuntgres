package reference_test

import (
	"stuntgres"
	"stuntgres/reference/fixture"
	"testing"
)

func TestReferenceConcurrentIndexSnapshotsNewer(t *testing.T) {
	fixture.ConcurrentIndexScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend) {
		a, newer, c, builder := fixture.ConcurrentIndexConn(t, p, b), fixture.ConcurrentIndexConn(t, p, b), fixture.ConcurrentIndexConn(t, p, b), fixture.ConcurrentIndexConn(t, p, b)
		ap, bp := fixture.ConcurrentIndexPID(t, p, b, a), fixture.ConcurrentIndexPID(t, p, b, builder)
		fixture.ConcurrentIndexExec(t, p, b, c, "CREATE TABLE t(n INTEGER)", 0)
		fixture.ConcurrentIndexExec(t, p, b, c, "INSERT INTO t VALUES(1)", 1)
		fixture.ConcurrentIndexExec(t, p, b, c, "CREATE TABLE u(n INTEGER)", 0)
		fixture.ConcurrentIndexExec(t, p, b, c, "INSERT INTO u VALUES(8)", 1)
		fixture.ConcurrentIndexExec(t, p, b, a, "BEGIN ISOLATION LEVEL REPEATABLE READ", 0)
		fixture.ConcurrentIndexRows(t, p, b, a, "SELECT n FROM u ORDER BY n", 8)
		w := fixture.ConcurrentIndexStart(t, p, builder, "CREATE INDEX CONCURRENTLY ix ON t(n)", false)
		fixture.ConcurrentIndexWait(t, p, b, "ix", stuntgres.IndexState{Ready: true, Live: true}, bp, ap, "waiting for old snapshots")
		// Advance actual committed visibility after validation; newer has no endangered xmin.
		fixture.ConcurrentIndexExec(t, p, b, c, "INSERT INTO u VALUES(9)", 1)
		fixture.ConcurrentIndexExec(t, p, b, newer, "BEGIN ISOLATION LEVEL REPEATABLE READ", 0)
		fixture.ConcurrentIndexRows(t, p, b, newer, "SELECT n FROM u ORDER BY n", 8, 9)
		fixture.ConcurrentIndexExec(t, p, b, a, "COMMIT", 0)
		if err := w.Join(t, p); err != nil {
			t.Fatal(err)
		}
		fixture.ConcurrentIndexWait(t, p, b, "ix", stuntgres.IndexState{Ready: true, Valid: true, Live: true}, 0, 0, "")
		fixture.ConcurrentIndexRows(t, p, b, newer, "SELECT n FROM u ORDER BY n", 8, 9)
		fixture.ConcurrentIndexExec(t, p, b, newer, "ROLLBACK", 0)
	})
}

func TestReferenceConcurrentIndexWritersCompatibleReaders(t *testing.T) {
	for _, rowShare := range []bool{false, true} {
		t.Run(fmtConcurrentCase(rowShare, false, false), func(t *testing.T) {
			fixture.ConcurrentIndexScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend) {
				a, c, builder := fixture.ConcurrentIndexConn(t, p, b), fixture.ConcurrentIndexConn(t, p, b), fixture.ConcurrentIndexConn(t, p, b)
				fixture.ConcurrentIndexExec(t, p, b, c, "CREATE TABLE t(n INTEGER)", 0)
				fixture.ConcurrentIndexExec(t, p, b, c, "INSERT INTO t VALUES(1)", 1)
				fixture.ConcurrentIndexExec(t, p, b, a, "BEGIN ISOLATION LEVEL READ COMMITTED", 0)
				fixture.ConcurrentIndexRows(t, p, b, a, "SELECT n FROM t ORDER BY n", 1)
				if rowShare {
					fixture.ConcurrentIndexExec(t, p, b, a, "LOCK TABLE t IN ROW SHARE MODE", 0)
				}
				w := fixture.ConcurrentIndexStart(t, p, builder, "CREATE INDEX CONCURRENTLY ix ON t(n)", true)
				if err := w.Join(t, p); err != nil {
					t.Fatal(err)
				}
				fixture.ConcurrentIndexWait(t, p, b, "ix", stuntgres.IndexState{Ready: true, Valid: true, Live: true}, 0, 0, "")
				fixture.ConcurrentIndexExec(t, p, b, a, "ROLLBACK", 0)
			})
		})
	}
}
