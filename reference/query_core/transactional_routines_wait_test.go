package reference_test

import (
	"context"
	"runtime"
	"testing"
	"time"

	"stuntgres/reference/fixture"
)

// Name uniqueness waits on transaction IDs; hard provider dependency admission
// instead uses the provider's actual PostgreSQL object lock. Do not substitute a
// relation OID or infer admission from a goroutine having started.
func transactionalRoutineObjectWait(t *testing.T, p *fixture.Pair, b fixture.Backend, waiter, holder int, class, object int64, finished <-chan struct{}) {
	t.Helper()
	if !b.PG {
		fixture.RelationLockPending(t, p, b, 0, 0, 0, "", finished)
		return
	}
	ctx, cancel := context.WithTimeout(p.Ctx, 5*time.Second)
	defer cancel()
	for {
		select {
		case <-finished:
			t.Fatal("provider operation completed before required admission")
		default:
		}
		var pending bool
		err := fixture.Admin.QueryRowContext(ctx, "SELECT $2=ANY(pg_blocking_pids($1)) AND EXISTS(SELECT 1 FROM pg_locks WHERE pid=$1 AND locktype='object' AND classid=$3 AND objid=$4 AND NOT granted)", waiter, holder, class, object).Scan(&pending)
		if err != nil {
			t.Fatal("provider object wait", err)
		}
		if pending {
			return
		}
		runtime.Gosched()
	}
}
