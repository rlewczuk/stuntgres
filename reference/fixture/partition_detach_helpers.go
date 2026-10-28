package fixture

import (
	"context"
	"testing"
	"time"
)

// PartitionDetachPending requires committed pending membership and the actual
// worker's virtual-transaction completion wait. It never treats completion as
// admission. Callers own cancellation and joining through LockOperation.
func PartitionDetachPending(t *testing.T, p *Pair, b Backend, parent, child string, waiter, holder int, finished <-chan struct{}) {
	t.Helper()
	VirtualCompletionPending(t, p, b, waiter, holder, finished)
	var pending bool
	if err := b.DB.QueryRowContext(p.Ctx, `SELECT inhdetachpending FROM pg_inherits WHERE inhparent=$1::regclass AND inhrelid=$2::regclass`, parent, child).Scan(&pending); err != nil || !pending {
		t.Fatal("committed pending membership", pending, err)
	}
	if b.PG {
		var forbidden bool
		if err := Admin.QueryRowContext(p.Ctx, `SELECT EXISTS(SELECT 1 FROM pg_locks WHERE pid=$1 AND relation=$2::regclass AND mode='AccessExclusiveLock')`, waiter, parent).Scan(&forbidden); err != nil || forbidden {
			t.Fatal("concurrent detach acquired or queued parent AX", forbidden, err)
		}
	}
}

// VirtualCompletionPending observes an exact oracle virtualxid/blocker pair or
// the managed native worker parked in the canonical transaction completion wait.
// It also serves FINALIZE's older-snapshot barrier without requiring relation AS.
func VirtualCompletionPending(t *testing.T, p *Pair, b Backend, waiter, holder int, finished <-chan struct{}) {
	t.Helper()
	ctx, cancel := context.WithTimeout(p.Ctx, 5*time.Second)
	defer cancel()
	tick := time.NewTicker(time.Millisecond)
	defer tick.Stop()
	stacks := make([]byte, 64<<10)
	for {
		select {
		case <-finished:
			t.Fatal("operation completed before virtual completion admission")
		case <-ctx.Done():
			t.Fatal("virtual completion admission", ctx.Err())
		default:
		}
		observed := false
		if b.PG {
			if err := Admin.QueryRowContext(ctx, `SELECT EXISTS(SELECT 1 FROM pg_locks WHERE pid=$1 AND locktype='virtualxid' AND NOT granted) AND $2=ANY(pg_blocking_pids($1))`, waiter, holder).Scan(&observed); err != nil {
				t.Fatal(err)
			}
		} else {
			observed, stacks = nativeOperationStack(stacks, []byte("(*relationRequest).wait"), []byte("(*statementHost).WaitTransaction"), []byte("fixture.LockOperation.func"))
		}
		if observed {
			return
		}
		select {
		case <-finished:
			t.Fatal("operation completed before virtual completion admission")
		case <-ctx.Done():
			t.Fatal("virtual completion admission", ctx.Err())
		case <-tick.C:
		}
	}
}
