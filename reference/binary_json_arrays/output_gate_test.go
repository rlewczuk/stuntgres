package reference_test

import (
	"context"
	"database/sql"
	"sync/atomic"
	"testing"
	"time"

	"stuntgres/reference/fixture"
)

// Resolve the physical owner through its assigned XID's genuine lock. End this
// temporary transaction before the held statement's implicit transaction begins.
func outputGatePID(t *testing.T, ctx context.Context, c *sql.Conn) int {
	t.Helper()
	tx, err := c.BeginTx(ctx, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer tx.Rollback()
	var xid string
	if err := tx.QueryRowContext(ctx, "SELECT pg_current_xact_id()::text").Scan(&xid); err != nil {
		t.Fatal(err)
	}
	var pid int
	if err := tx.QueryRowContext(ctx, "SELECT pid FROM pg_locks WHERE locktype='transactionid' AND transactionid=$1::xid AND granted AND mode='ExclusiveLock'", xid).Scan(&pid); err != nil || pid <= 0 {
		t.Fatal("output gate owner", pid, err)
	}
	if err := tx.Rollback(); err != nil {
		t.Fatal(err)
	}
	return pid
}

// SQL producers map actual output to key 2 (small) or 67108864 (large).
// The holder makes admission persistent until cancellation and join; callers
// may also require the earlier small-output grant, without publishing a row.
// This observes active SQL after output, not interruption inside a provider.
func outputGateCancel(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn, requireSmallGrant bool, operation func(context.Context) fixture.RelationOutcome) {
	t.Helper()
	start := time.Now()
	var phases struct {
		borrow, workerPID, holderPID, begin, acquire [2]time.Time
		join, cleanup, rollback, released            [2]time.Time
		launch, admitted, cancelStart, cancelEnd     time.Time
		firstQuery, lastQueryStart, lastQueryEnd     time.Time
	}
	var callbackEntry, callbackReturn atomic.Int64
	var polls int
	var queryTotal, queryMax time.Duration
	var lastAdmitted, queryInFlight bool
	var lastQueryErr error
	// Registered first: failure cleanup and joins run before this snapshot.
	defer func() {
		if !t.Failed() {
			return
		}
		elapsed := func(at time.Time) any {
			if at.IsZero() {
				return "unset"
			}
			return at.Sub(start)
		}
		bracket := func(pair [2]time.Time) [2]any {
			return [2]any{elapsed(pair[0]), elapsed(pair[1])}
		}
		callback := func(n int64) any {
			if n == 0 {
				return "unset"
			}
			return time.Duration(n - 1)
		}
		parentDeadline, parentBound := p.Ctx.Deadline()
		t.Logf("output gate phases elapsed=%s parentDeadline=%v bounded=%t parentRemaining=%s borrow=%v workerPID=%v holderPID=%v begin=%v acquire=%v launch=%v callback=[%v %v] admitted=%v cancel=[%v %v] join=%v cleanup=%v rollback=%v released=%v",
			time.Since(start), parentDeadline, parentBound, time.Until(parentDeadline),
			bracket(phases.borrow), bracket(phases.workerPID), bracket(phases.holderPID),
			bracket(phases.begin), bracket(phases.acquire), elapsed(phases.launch),
			callback(callbackEntry.Load()), callback(callbackReturn.Load()), elapsed(phases.admitted),
			elapsed(phases.cancelStart), elapsed(phases.cancelEnd), bracket(phases.join),
			bracket(phases.cleanup), bracket(phases.rollback), bracket(phases.released))
		t.Logf("output gate observations count=%d first=%v last=[%v %v] total=%s max=%s inFlight=%t lastAdmitted=%t lastError=%v (callback is API entry; query durations include scheduling)",
			polls, elapsed(phases.firstQuery), elapsed(phases.lastQueryStart),
			elapsed(phases.lastQueryEnd), queryTotal, queryMax, queryInFlight, lastAdmitted, lastQueryErr)
	}()
	ctx, cancel := context.WithTimeout(p.Ctx, 15*time.Second)
	defer cancel()
	defer func() {
		if t.Failed() {
			deadline, _ := ctx.Deadline()
			t.Logf("output gate admission entry=%s deadline=%v remaining=%s error=%v", start.Format(time.RFC3339Nano), deadline, time.Until(deadline), ctx.Err())
		}
	}()
	phases.borrow[0] = time.Now()
	holder, observer := fixture.LockConnections(t, p.Ctx, b.DB)
	phases.borrow[1] = time.Now()
	phases.workerPID[0] = time.Now()
	workerPID := outputGatePID(t, ctx, c)
	phases.workerPID[1] = time.Now()
	phases.holderPID[0] = time.Now()
	holderPID := outputGatePID(t, ctx, holder)
	phases.holderPID[1] = time.Now()
	phases.begin[0] = time.Now()
	tx, err := holder.BeginTx(p.Ctx, nil)
	phases.begin[1] = time.Now()
	if err != nil {
		t.Fatal(err)
	}
	released := false
	defer func() {
		if !released {
			phases.rollback[0] = time.Now()
			if err := tx.Rollback(); err != nil {
				t.Error("output gate holder rollback", err)
			}
			phases.rollback[1] = time.Now()
		}
	}()
	var locked any
	phases.acquire[0] = time.Now()
	if err := tx.QueryRowContext(ctx, "SELECT pg_advisory_xact_lock(67108864::bigint)").Scan(&locked); err != nil {
		t.Fatal(err)
	}
	phases.acquire[1] = time.Now()
	done := make(chan fixture.RelationOutcome, 1)
	joined := false
	// Registered after holder rollback so even assertion-failure exits cancel
	// and bounded-join the worker before releasing its blocker or caller handles.
	defer func() {
		cancel()
		if !joined {
			phases.join[0] = time.Now()
			out := fixture.LockResult(t, done)
			phases.join[1] = time.Now()
			joined = true
			if out.Rows != nil {
				if err := out.Rows.Close(); err != nil {
					t.Error("output gate failure cursor close", err)
				}
			}
		}
	}()
	phases.launch = time.Now()
	go func() {
		callbackEntry.Store(time.Since(start).Nanoseconds() + 1)
		out := operation(ctx)
		callbackReturn.Store(time.Since(start).Nanoseconds() + 1)
		done <- out
	}()
	var poll *time.Timer
	defer func() {
		if poll != nil {
			poll.Stop()
		}
	}()
	for {
		select {
		case out := <-done:
			joined = true
			phases.join = [2]time.Time{time.Now(), time.Now()}
			if out.Rows != nil {
				if err := out.Rows.Close(); err != nil {
					t.Error("output gate early cursor close", err)
				}
			}
			t.Fatal("operation completed before public output gate", out.Err)
		default:
		}
		// All facts are required together: exact worker wait, actual blocker,
		// matching holder grant, and (for ordered INSERT) the earlier small grant.
		var admitted bool
		phases.lastQueryStart = time.Now()
		phases.lastQueryEnd = time.Time{}
		if polls == 0 {
			phases.firstQuery = phases.lastQueryStart
		}
		polls++
		queryInFlight = true
		err := observer.QueryRowContext(ctx, `SELECT
			EXISTS(SELECT 1 FROM pg_locks WHERE pid=$1 AND locktype='advisory'
				AND database=(SELECT oid FROM pg_database WHERE datname=current_database())
				AND classid::bigint=0 AND objid::bigint=67108864 AND objsubid=1
				AND mode='ExclusiveLock' AND NOT granted)
			AND $2=ANY(pg_blocking_pids($1))
			AND EXISTS(SELECT 1 FROM pg_locks WHERE pid=$2 AND locktype='advisory'
				AND database=(SELECT oid FROM pg_database WHERE datname=current_database())
				AND classid::bigint=0 AND objid::bigint=67108864 AND objsubid=1
				AND mode='ExclusiveLock' AND granted)
			AND (NOT $3 OR EXISTS(SELECT 1 FROM pg_locks WHERE pid=$1 AND locktype='advisory'
				AND database=(SELECT oid FROM pg_database WHERE datname=current_database())
				AND classid::bigint=0 AND objid::bigint=2 AND objsubid=1
				AND mode='ExclusiveLock' AND granted))`, workerPID, holderPID, requireSmallGrant).Scan(&admitted)
		phases.lastQueryEnd = time.Now()
		queryInFlight = false
		duration := phases.lastQueryEnd.Sub(phases.lastQueryStart)
		queryTotal += duration
		if duration > queryMax {
			queryMax = duration
		}
		lastAdmitted, lastQueryErr = admitted, err
		if err != nil {
			t.Fatal("public output gate admission", err)
		}
		if err := ctx.Err(); err != nil {
			t.Fatal("public output gate deadline", err)
		}
		if admitted {
			phases.admitted = time.Now()
			break
		}
		// Pace misses only; a timer event never supplies admission.
		if poll == nil {
			poll = time.NewTimer(time.Millisecond)
		} else {
			poll.Reset(time.Millisecond)
		}
		select {
		case <-ctx.Done():
			t.Fatal("public output gate deadline", ctx.Err())
		case out := <-done:
			joined = true
			phases.join = [2]time.Time{time.Now(), time.Now()}
			if out.Rows != nil {
				if err := out.Rows.Close(); err != nil {
					t.Error("output gate early cursor close", err)
				}
			}
			t.Fatal("operation completed before public output gate", out.Err)
		case <-poll.C:
		}
	}
	phases.cancelStart = time.Now()
	if b.PG {
		var signaled bool
		if err := observer.QueryRowContext(ctx, "SELECT pg_cancel_backend($1)", workerPID).Scan(&signaled); err != nil || !signaled {
			t.Fatal("output gate backend cancellation", signaled, err)
		}
	} else {
		cancel()
	}
	phases.cancelEnd = time.Now()
	phases.join[0] = time.Now()
	out := fixture.LockResult(t, done)
	phases.join[1] = time.Now()
	joined = true
	if out.Rows != nil {
		if err := out.Rows.Close(); err != nil {
			t.Error("output gate canceled cursor close", err)
		}
		t.Fatal("active canceled output gate published cursor")
	}
	if out.Result != nil {
		t.Fatal("active canceled output gate published result")
	}
	if b.PG {
		fixture.AssertDiagnostic(t, out.Err, &fixture.Diagnostic{Severity: "ERROR", SeverityUnlocalized: "ERROR", Code: "57014", Message: "canceling statement due to user request"})
	} else {
		fixture.LexicalOrdinary(t, out.Err, context.Canceled)
	}
	// Inspect requests AND grants using a live idle observer, never the canceled
	// worker context. Check both keys even when read order was unspecified.
	cleanupCtx, cleanupCancel := context.WithTimeout(p.Ctx, 5*time.Second)
	defer cleanupCancel()
	const gateLocks = `SELECT count(*) FROM pg_locks WHERE pid IN ($1,$2)
		AND locktype='advisory'
		AND database=(SELECT oid FROM pg_database WHERE datname=current_database())
		AND classid::bigint=0 AND objid::bigint IN (2,67108864) AND objsubid=1`
	var remaining int64
	phases.cleanup[0] = time.Now()
	if err := observer.QueryRowContext(cleanupCtx, gateLocks, workerPID, workerPID).Scan(&remaining); err != nil || remaining != 0 {
		t.Fatal("canceled worker retained output gate request or grant", remaining, err)
	}
	phases.cleanup[1] = time.Now()
	phases.rollback[0] = time.Now()
	if err := tx.Rollback(); err != nil {
		t.Fatal("output gate holder rollback", err)
	}
	phases.rollback[1] = time.Now()
	released = true
	phases.released[0] = time.Now()
	if err := observer.QueryRowContext(cleanupCtx, gateLocks, workerPID, holderPID).Scan(&remaining); err != nil || remaining != 0 {
		t.Fatal("released output gate retained request or grant", remaining, err)
	}
	phases.released[1] = time.Now()
}
