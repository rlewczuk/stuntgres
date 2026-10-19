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
	activeProviderCancel(t, p, b, c, query, "(*relationRequest).wait", "", func(ctx context.Context) {
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
