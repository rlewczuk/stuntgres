package fixture

import (
	"context"
	"database/sql"
	"runtime"
	"testing"
	"time"
)

func LockPID(t *testing.T, ctx context.Context, conn *sql.Conn) int {
	t.Helper()
	var pid int
	if err := conn.QueryRowContext(ctx, "SELECT pg_backend_pid()").Scan(&pid); err != nil {
		t.Fatal(err)
	}
	if pid <= 0 {
		t.Fatal("invalid backend PID", pid)
	}
	return pid
}

// RelationLockActiveCancel observes this owner's actual queued operation before
// cancellation, retaining the connection for complete post-failure assertions.
func RelationLockActiveCancel(t *testing.T, p *Pair, b Backend, c *sql.Conn, query string, operation func(context.Context) RelationOutcome) {
	t.Helper()
	pid := 0
	if b.PG {
		pid = LockPID(t, p.Ctx, c)
	}
	activeProviderCancel(t, p, b, c, query, "(*relationRequest).wait", "", "", func(ctx context.Context) {
		if b.PG {
			LockWait(t, ctx, pid)
		}
	}, operation)
}

func LockOID(t *testing.T, ctx context.Context, name string) int64 {
	t.Helper()
	var oid int64
	if err := Admin.QueryRowContext(ctx, "SELECT $1::regclass::oid", name).Scan(&oid); err != nil {
		t.Fatal(err)
	}
	if oid <= 0 || oid > 1<<32-1 {
		t.Fatal("invalid relation OID", oid)
	}
	return oid
}

// LockWaitBehind returns after observing the exact queued lock, or after the
// optional actual-operation completion signal closes. A self-timing SQL command
// can finish before its transient wait is observed; its caller must still join
// and validate the real outcome. Without a signal, queue observation is required.
// External-cancellation admission barriers must retain that strict form.
func LockWaitBehind(t *testing.T, ctx context.Context, waiter, holder int, relation int64, mode string, completed ...<-chan struct{}) {
	t.Helper()
	if waiter <= 0 || holder <= 0 || waiter == holder {
		t.Fatal("invalid blocker identities", waiter, holder)
	}
	ctx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	var done <-chan struct{}
	if len(completed) != 0 {
		done = completed[0]
	}
	for {
		select {
		case <-done:
			return
		default:
		}
		var waiting bool
		err := Admin.QueryRowContext(ctx, `SELECT EXISTS(SELECT 1 FROM pg_locks WHERE pid=$1 AND locktype='relation' AND relation=$4 AND mode=$3 AND NOT granted) AND $2 = ANY(pg_blocking_pids($1))`, waiter, holder, mode, relation).Scan(&waiting)
		if err != nil {
			select {
			case <-done:
				return
			default:
				t.Fatal(err)
			}
		}
		if waiting {
			return
		}
		runtime.Gosched()
	}
}

func LockWait(t *testing.T, ctx context.Context, pid int) {
	t.Helper()
	for {
		var waiting bool
		err := Admin.QueryRowContext(ctx, `SELECT EXISTS(SELECT 1 FROM pg_locks WHERE pid=$1 AND locktype='relation' AND NOT granted) AND cardinality(pg_blocking_pids($1)) > 0`, pid).Scan(&waiting)
		if err != nil {
			t.Fatal(err)
		}
		if waiting {
			return
		}
		if ctx.Err() != nil {
			t.Fatal(ctx.Err())
		}
		runtime.Gosched()
	}
}

// RelationLockPending observes a real LockOperation worker parked in the native
// relation scheduler, or the exact PostgreSQL relation/mode/blocker tuple.
// The worker's completion signal prevents mistaking an already-finished command
// for admission. It does not cancel or consume the operation.
func RelationLockPending(t *testing.T, p *Pair, b Backend, waiter, holder int, relation int64, mode string, finished <-chan struct{}) {
	t.Helper()
	if b.PG {
		if relation != 0 {
			LockWaitBehind(t, p.Ctx, waiter, holder, relation, mode)
		} else {
			ctx, cancel := context.WithTimeout(p.Ctx, 5*time.Second)
			defer cancel()
			for {
				var pending bool
				if err := Admin.QueryRowContext(ctx, `SELECT EXISTS(SELECT 1 FROM pg_locks WHERE pid=$1 AND locktype='transactionid' AND NOT granted) AND $2=ANY(pg_blocking_pids($1))`, waiter, holder).Scan(&pending); err != nil {
					t.Fatal(err)
				}
				if pending {
					break
				}
			}
		}
		return
	}
	ctx, cancel := context.WithTimeout(p.Ctx, 5*time.Second)
	defer cancel()
	stacks := make([]byte, 64<<10)
	poll := time.NewTicker(time.Millisecond)
	defer poll.Stop()
	for {
		select {
		case <-finished:
			t.Fatal("operation completed before required scheduler wait")
		case <-ctx.Done():
			t.Fatal("native scheduler admission", ctx.Err())
		case <-poll.C:
		}
		var observed bool
		observed, stacks = nativeOperationStack(stacks, []byte("(*relationRequest).wait"), []byte("LockOperation"))
		if observed {
			return
		}
	}
}

// NativeRelationLockWait observes the actual managed worker parked in relation
// admission, rather than inferring a wait from worker launch or elapsed time.
func NativeRelationLockWait(t *testing.T, ctx context.Context, completed <-chan struct{}) {
	t.Helper()
	nativeOperationRowWait(t, ctx, completed, "(*StatementScope).acquireRelationTarget", "fixture.LockOperation.func")
}

// NativeCopyRelationLockWait observes the real public COPY helper parked in
// initial relation admission. COPY does not run inside fixture.LockOperation.
func NativeCopyRelationLockWait(t *testing.T, ctx context.Context, completed <-chan struct{}) {
	t.Helper()
	nativeOperationRowWait(t, ctx, completed, "(*StatementScope).acquireRelationTarget", "(*conn).copyRequest")
}

// NativeCopyUpdateRowWait observes the public COPY worker blocked on an actual
// UPDATE row lock, including an UPDATE executed by its queued AFTER triggers.
func NativeCopyUpdateRowWait(t *testing.T, ctx context.Context, completed <-chan struct{}) {
	t.Helper()
	nativeOperationRowWait(t, ctx, completed, "(*StatementScope).acquireUpdateRowMode", "(*conn).copyRequest")
}
