package fixture

import (
	"context"
	"runtime"
	"testing"
	"time"
)

// ObjectLockPending requires the actual nonpinned dependency endpoint wait.
// It neither releases the holder nor consumes/cancels the LockOperation worker.
// Callers retain cancellation/join ownership on every exit.
func ObjectLockPending(t *testing.T, p *Pair, b Backend, waiter, holder int, class, object int64, mode string, finished <-chan struct{}) {
	t.Helper()
	ctx, cancel := context.WithTimeout(p.Ctx, 5*time.Second)
	defer cancel()
	stacks := make([]byte, 64<<10)
	for {
		select {
		case <-finished:
			t.Fatal("operation completed before required object lock wait")
		case <-ctx.Done():
			t.Fatal("object lock admission", ctx.Err())
		default:
		}
		if b.PG {
			var pending bool
			if err := Admin.QueryRowContext(ctx, `SELECT EXISTS(SELECT 1 FROM pg_locks WHERE pid=$1 AND locktype='object' AND classid=$3::oid AND objid=$4::oid AND objsubid=0 AND mode=$5 AND NOT granted) AND $2=ANY(pg_blocking_pids($1))`, waiter, holder, class, object, mode).Scan(&pending); err != nil {
				t.Fatal(err)
			}
			if pending {
				return
			}
		} else {
			var observed bool
			observed, stacks = nativeOperationStack(stacks, []byte("(*relationRequest).wait"), []byte("LockOperation"))
			if observed {
				// A parked worker alone is not enough: require the exact real object tag,
				// waiter mode and an actual granted blocker from the same manager.
				var pending bool
				if err := b.DB.QueryRowContext(ctx, `SELECT EXISTS(SELECT 1 FROM pg_locks w JOIN pg_locks h ON h.locktype=w.locktype AND h.classid=w.classid AND h.objid=w.objid AND h.objsubid=w.objsubid WHERE w.locktype='object' AND w.classid=$1::oid AND w.objid=$2::oid AND w.objsubid=0 AND w.mode=$3 AND NOT w.granted AND h.granted AND h.pid=ANY(pg_blocking_pids(w.pid)))`, class, object, mode).Scan(&pending); err != nil {
					t.Fatal(err)
				}
				if pending {
					return
				}
			}
		}
		runtime.Gosched()
	}
}
