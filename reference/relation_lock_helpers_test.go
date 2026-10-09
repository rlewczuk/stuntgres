package reference_test

import (
	"context"
	"database/sql"
	"runtime"
	"testing"
	"time"
)

func referenceLockPID(t *testing.T, ctx context.Context, conn *sql.Conn) int {
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

func referenceLockOID(t *testing.T, ctx context.Context, name string) int64 {
	t.Helper()
	var oid int64
	if err := referenceAdmin.QueryRowContext(ctx, "SELECT $1::regclass::oid", name).Scan(&oid); err != nil {
		t.Fatal(err)
	}
	if oid <= 0 || oid > 1<<32-1 {
		t.Fatal("invalid relation OID", oid)
	}
	return oid
}

func referenceLockWaitBehind(t *testing.T, ctx context.Context, waiter, holder int, relation int64, mode string) {
	t.Helper()
	if waiter <= 0 || holder <= 0 || waiter == holder {
		t.Fatal("invalid blocker identities", waiter, holder)
	}
	ctx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	for {
		var waiting bool
		err := referenceAdmin.QueryRowContext(ctx, `SELECT EXISTS(SELECT 1 FROM pg_locks WHERE pid=$1 AND locktype='relation' AND relation=$4 AND mode=$3 AND NOT granted) AND $2 = ANY(pg_blocking_pids($1))`, waiter, holder, mode, relation).Scan(&waiting)
		if err != nil {
			t.Fatal(err)
		}
		if waiting {
			return
		}
		runtime.Gosched()
	}
}

func referenceLockWait(t *testing.T, ctx context.Context, pid int) {
	t.Helper()
	for {
		var waiting bool
		err := referenceAdmin.QueryRowContext(ctx, `SELECT EXISTS(SELECT 1 FROM pg_locks WHERE pid=$1 AND locktype='relation' AND NOT granted) AND cardinality(pg_blocking_pids($1)) > 0`, pid).Scan(&waiting)
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
