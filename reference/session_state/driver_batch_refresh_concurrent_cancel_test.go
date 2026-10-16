package reference_test

import (
	"bytes"
	"context"
	"database/sql"
	"errors"
	"runtime"
	"strings"
	"stuntgres/reference/fixture"
	"testing"
	"time"
)

type concurrentBatchOutcome struct {
	result sql.Result
	err    error
}

func concurrentBatchWorker(ctx context.Context, conn *sql.Conn, q string, done chan<- concurrentBatchOutcome) {
	result, err := conn.ExecContext(ctx, q)
	done <- concurrentBatchOutcome{result, err}
}
func concurrentBatchNativeSourceWait(t *testing.T, ctx context.Context, done <-chan concurrentBatchOutcome, joined *bool) {
	t.Helper()
	profiles := make([]runtime.StackRecord, 128)
	for {
		select {
		case outcome := <-done:
			*joined = true
			t.Fatal("retained worker finished before source wait", outcome.err)
		case <-ctx.Done():
			t.Fatal(ctx.Err())
		default:
		}
		n, ok := runtime.GoroutineProfile(profiles)
		if !ok {
			profiles = make([]runtime.StackRecord, n)
			continue
		}
		for _, profile := range profiles[:n] {
			frames := runtime.CallersFrames(profile.Stack())
			wait, source, worker := false, false, false
			for {
				frame, more := frames.Next()
				wait = wait || strings.HasSuffix(frame.Function, ".(*relationRequest).wait")
				source = source || strings.HasSuffix(frame.Function, ".acquireBoundViewRelationModes")
				worker = worker || strings.HasSuffix(frame.Function, ".concurrentBatchWorker")
				if !more {
					break
				}
			}
			if wait && source && worker {
				return
			}
		}
		runtime.Gosched()
	}
}

func TestReferenceMaterializedRefreshConcurrentRetainedCancellation(t *testing.T) {
	concurrentBatchScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, f fixture.ControlsReferenceFixture) {
		concurrentBatchSeed(t, p, b, f, false)
		batchExec(t, p, b, f.A, "CREATE TABLE batch_concurrent_blocked_base(i INT,s TEXT)", 0)
		batchExec(t, p, b, f.A, "INSERT INTO batch_concurrent_blocked_base VALUES(7,'blocked old')", 1)
		batchExec(t, p, b, f.A, "CREATE MATERIALIZED VIEW batch_concurrent_blocked AS SELECT i,s FROM batch_concurrent_blocked_base", 1)
		batchExec(t, p, b, f.A, "CREATE UNIQUE INDEX batch_concurrent_blocked_u ON batch_concurrent_blocked(i)", 0)
		observer, err := f.DB.Conn(p.Ctx)
		if err != nil {
			t.Fatal(err)
		}
		defer observer.Close()
		var before []byte
		if !b.PG {
			before = concurrentBatchDump(t, p)
		}
		holder := fixture.QueryLockBegin(t, p.Ctx, f.B)
		fixture.RunStep(t, p.Ctx, holder, b.Recorder, fixture.Exec("LOCK TABLE batch_concurrent_blocked_base IN ACCESS EXCLUSIVE MODE", 0))
		ctx, cancel := context.WithTimeout(p.Ctx, 15*time.Second)
		defer cancel()
		done := make(chan concurrentBatchOutcome, 1)
		joined := false
		pid, blocker := 0, 0
		var oid int64
		if b.PG {
			pid = fixture.LockPID(t, p.Ctx, f.A)
			blocker = fixture.LockPID(t, p.Ctx, f.B)
			oid = fixture.LockOID(t, p.Ctx, "batch_concurrent_blocked_base")
		}
		defer func() {
			cancel()
			holder.Rollback()
			if !joined {
				cleanup, stop := context.WithTimeout(context.Background(), 5*time.Second)
				defer stop()
				select {
				case <-done:
				case <-cleanup.Done():
					t.Error("retained cancel cleanup", cleanup.Err())
				}
			}
		}()
		q := concurrentBatchChange + concurrentBatchRefresh + "; REFRESH MATERIALIZED VIEW CONCURRENTLY batch_concurrent_blocked; INSERT INTO batch_concurrent_base VALUES(99,'skipped')"
		go concurrentBatchWorker(ctx, f.A, q, done)
		if b.PG {
			fixture.LockWaitBehind(t, ctx, pid, blocker, oid, "AccessShareLock")
		} else {
			concurrentBatchNativeSourceWait(t, ctx, done, &joined)
		}
		batchRefreshRows(t, p, b, observer, "batch_concurrent_mv", concurrentBatchOld())
		batchRefreshRows(t, p, b, observer, "batch_concurrent_blocked", [][]any{{int64(7), "blocked old"}})
		if b.PG {
			var signaled bool
			if err := fixture.Admin.QueryRowContext(ctx, "SELECT pg_cancel_backend($1)", pid).Scan(&signaled); err != nil || !signaled {
				t.Fatal("retained backend cancel", signaled, err)
			}
		} else {
			cancel()
		}
		join, stop := context.WithTimeout(context.Background(), 5*time.Second)
		defer stop()
		select {
		case outcome := <-done:
			joined = true
			if outcome.result != nil {
				t.Fatal("retained canceled result")
			}
			if b.PG {
				d := batchDiag("57014", "canceling statement due to user request")
				fixture.AssertDiagnostic(t, outcome.err, &d)
			} else if !errors.Is(outcome.err, context.Canceled) {
				t.Fatal("retained cancel error", outcome.err)
			}
		case <-join.Done():
			t.Fatal("retained cancel join", join.Err())
		}
		if err := holder.Rollback(); err != nil {
			t.Fatal(err)
		}
		batchRefreshRows(t, p, b, observer, "batch_concurrent_mv", concurrentBatchOld())
		batchRefreshRows(t, p, b, observer, "batch_concurrent_base", concurrentBatchOld())
		batchRefreshPopulation(t, p, b, observer, "batch_concurrent_mv", true)
		if !b.PG && !bytes.Equal(before, concurrentBatchDump(t, p)) {
			t.Fatal("retained cancellation changed Dump")
		}
		batchExec(t, p, b, observer, concurrentBatchChange+concurrentBatchRefresh+"; REFRESH MATERIALIZED VIEW CONCURRENTLY batch_concurrent_blocked", 0)
		batchRefreshRows(t, p, b, observer, "batch_concurrent_mv", concurrentBatchNew())
	})
}
