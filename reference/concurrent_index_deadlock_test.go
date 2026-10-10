package reference_test

import (
	"context"
	"database/sql"
	"stuntgres"
	"testing"
	"time"
)

func TestReferenceConcurrentIndexSerializationDeadlock(t *testing.T) {
	concurrentIndexScenario(t, func(t *testing.T, p *referencePair, b referenceBackend) {
		a, c, builder := concurrentIndexConn(t, p, b), concurrentIndexConn(t, p, b), concurrentIndexConn(t, p, b)
		ap, bp := concurrentIndexPID(t, p, b, a), concurrentIndexPID(t, p, b, builder)
		concurrentIndexExec(t, p, b, c, "CREATE TABLE t(n INTEGER)", 0)
		concurrentIndexExec(t, p, b, c, "INSERT INTO t VALUES(1)", 1)
		concurrentIndexExec(t, p, b, a, "BEGIN", 0)
		concurrentIndexExec(t, p, b, a, "INSERT INTO t VALUES(2)", 1)
		var oid, database int64
		var vxid string
		if b.pg {
			oid = referenceLockOID(t, p.ctx, "t")
			if err := referenceAdmin.QueryRowContext(p.ctx, "SELECT oid FROM pg_database WHERE datname=current_database()").Scan(&database); err != nil {
				t.Fatal(err)
			}
			if err := referenceAdmin.QueryRowContext(p.ctx, "SELECT virtualtransaction FROM pg_locks WHERE pid=$1 AND locktype='virtualxid' AND granted", ap).Scan(&vxid); err != nil {
				t.Fatal(err)
			}
		}
		w := concurrentIndexStart(t, p, builder, "CREATE INDEX CONCURRENTLY ix ON t(n)", true)
		concurrentIndexWait(t, p, b, "ix", stuntgres.IndexState{Live: true}, bp, ap, "waiting for writers before build")
		ctx, cancel := context.WithCancel(p.ctx)
		defer cancel()
		type outcome struct {
			result sql.Result
			err    error
		}
		done := make(chan outcome, 1)
		go func() {
			r, e := a.ExecContext(ctx, "LOCK TABLE t IN ACCESS EXCLUSIVE MODE")
			done <- outcome{r, e}
			close(done)
		}()
		t.Cleanup(func() {
			cancel()
			select {
			case <-done:
			case <-time.After(5 * time.Second):
				t.Error("deadlock AX worker did not join")
			}
		})
		// Select only to drain; identify the victim by SQLSTATE, never completion order.
		var ax outcome
		var buildErr error
		select {
		case ax = <-done:
			buildErr = w.join(t, p)
		case buildErr = <-w.done:
			select {
			case ax = <-done:
			case <-p.ctx.Done():
				t.Fatal(p.ctx.Err())
			}
		case <-p.ctx.Done():
			t.Fatal(p.ctx.Err())
		}
		victim := -1
		for i, err := range []error{ax.err, buildErr} {
			if err != nil {
				d := referenceExtractDiagnostic(t, err)
				if d.Code != "40P01" || victim != -1 {
					t.Fatalf("one deadlock victim: AX=%v build=%v", ax.err, buildErr)
				}
				victim = i
			}
		}
		if victim < 0 {
			t.Fatal("deadlock had no victim")
		}
		err := ax.err
		if victim == 1 {
			err = buildErr
		}
		if b.pg {
			referenceDeadlockPhysical(t, err, []int{ap, bp}, map[int64]bool{oid: true}, database, map[string]int{vxid: ap}, victim, "completion", "")
		} else {
			deadlockPublicError(t, err, "")
		}
		if victim == 0 {
			if buildErr != nil {
				t.Fatal(buildErr)
			}
			concurrentIndexWait(t, p, b, "ix", stuntgres.IndexState{Ready: true, Valid: true, Live: true}, 0, 0, "")
			concurrentIndexExec(t, p, b, a, "ROLLBACK", 0)
			concurrentIndexRows(t, p, b, c, "SELECT n FROM t ORDER BY n", 1)
		} else {
			if ax.err != nil {
				t.Fatal(ax.err)
			}
			referenceLockAffected(t, ax.result, 0)
			concurrentIndexExec(t, p, b, a, "COMMIT", 0)
			concurrentIndexWait(t, p, b, "ix", stuntgres.IndexState{Live: true}, 0, 0, "")
			concurrentIndexRows(t, p, b, c, "SELECT n FROM t ORDER BY n", 1, 2)
		}
		concurrentIndexExec(t, p, b, c, "DROP INDEX ix", 0)
		concurrentIndexExec(t, p, b, c, "CREATE INDEX CONCURRENTLY ix ON t(n)", 0)
	})
}
