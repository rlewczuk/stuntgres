package reference_test

import (
	"context"
	"database/sql"
	"stuntgres"
	"stuntgres/reference/fixture"
	"testing"
	"time"
)

func TestReferenceConcurrentIndexSerializationDeadlock(t *testing.T) {
	fixture.ConcurrentIndexScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend) {
		a, c, builder := fixture.ConcurrentIndexConn(t, p, b), fixture.ConcurrentIndexConn(t, p, b), fixture.ConcurrentIndexConn(t, p, b)
		ap, bp := fixture.ConcurrentIndexPID(t, p, b, a), fixture.ConcurrentIndexPID(t, p, b, builder)
		fixture.ConcurrentIndexExec(t, p, b, c, "CREATE TABLE t(n INTEGER)", 0)
		fixture.ConcurrentIndexExec(t, p, b, c, "INSERT INTO t VALUES(1)", 1)
		fixture.ConcurrentIndexExec(t, p, b, a, "BEGIN", 0)
		fixture.ConcurrentIndexExec(t, p, b, a, "INSERT INTO t VALUES(2)", 1)
		var oid, database int64
		var vxid string
		if b.PG {
			oid = fixture.LockOID(t, p.Ctx, "t")
			if err := fixture.Admin.QueryRowContext(p.Ctx, "SELECT oid FROM pg_database WHERE datname=current_database()").Scan(&database); err != nil {
				t.Fatal(err)
			}
			if err := fixture.Admin.QueryRowContext(p.Ctx, "SELECT virtualtransaction FROM pg_locks WHERE pid=$1 AND locktype='virtualxid' AND granted", ap).Scan(&vxid); err != nil {
				t.Fatal(err)
			}
		}
		w := fixture.ConcurrentIndexStart(t, p, builder, "CREATE INDEX CONCURRENTLY ix ON t(n)", true)
		fixture.ConcurrentIndexWait(t, p, b, "ix", stuntgres.IndexState{Live: true}, bp, ap, "waiting for writers before build")
		ctx, cancel := context.WithCancel(p.Ctx)
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
			buildErr = w.Join(t, p)
		case buildErr = <-w.Done:
			select {
			case ax = <-done:
			case <-p.Ctx.Done():
				t.Fatal(p.Ctx.Err())
			}
		case <-p.Ctx.Done():
			t.Fatal(p.Ctx.Err())
		}
		victim := -1
		for i, err := range []error{ax.err, buildErr} {
			if err != nil {
				d := fixture.ExtractDiagnostic(t, err)
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
		if b.PG {
			fixture.DeadlockPhysical(t, err, []int{ap, bp}, map[int64]bool{oid: true}, database, map[string]int{vxid: ap}, victim, "completion", "")
		} else {
			fixture.DeadlockPublicError(t, err, "")
		}
		if victim == 0 {
			if buildErr != nil {
				t.Fatal(buildErr)
			}
			fixture.ConcurrentIndexWait(t, p, b, "ix", stuntgres.IndexState{Ready: true, Valid: true, Live: true}, 0, 0, "")
			fixture.ConcurrentIndexExec(t, p, b, a, "ROLLBACK", 0)
			fixture.ConcurrentIndexRows(t, p, b, c, "SELECT n FROM t ORDER BY n", 1)
		} else {
			if ax.err != nil {
				t.Fatal(ax.err)
			}
			fixture.LockAffected(t, ax.result, 0)
			fixture.ConcurrentIndexExec(t, p, b, a, "COMMIT", 0)
			fixture.ConcurrentIndexWait(t, p, b, "ix", stuntgres.IndexState{Live: true}, 0, 0, "")
			fixture.ConcurrentIndexRows(t, p, b, c, "SELECT n FROM t ORDER BY n", 1, 2)
		}
		fixture.ConcurrentIndexExec(t, p, b, c, "DROP INDEX ix", 0)
		fixture.ConcurrentIndexExec(t, p, b, c, "CREATE INDEX CONCURRENTLY ix ON t(n)", 0)
	})
}
