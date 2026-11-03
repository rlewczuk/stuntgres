package fixture

import (
	"context"
	"testing"
	"time"
)

// ReplicationOriginDropWait observes the same still-live SQL DROP worker before
// its owner is released or the request is canceled. Neither worker creation nor
// elapsed time establishes admission. Native observation is test-only live-stack
// evidence; PostgreSQL supplies its actual wait event for the exact PID/query.
func ReplicationOriginDropWait(t *testing.T, p *Pair, b Backend, pid int, query string, completed <-chan struct{}) {
	t.Helper()
	ctx, cancel := context.WithTimeout(p.Ctx, Liveness)
	defer cancel()
	stacks := make([]byte, 64<<10)
	symbols := [][]byte{
		[]byte("[select"),
		[]byte("(*Engine).waitOriginReleaseLocked.func1"),
		[]byte("(*Engine).waitOriginReleaseLocked("),
		[]byte("DDLHost.ClearReplicationOrigin"),
		[]byte("subscription.Stage"),
		[]byte("fixture.LockOperation.func"),
	}
	var poll *time.Timer
	defer func() {
		if poll != nil {
			poll.Stop()
		}
	}()
	for {
		select {
		case <-completed:
			t.Fatal("SQL DROP completed before replication-origin wait admission")
		case <-ctx.Done():
			t.Fatalf("replication-origin DROP wait admission: %v\n%s", ctx.Err(), stacks)
		default:
		}
		var observed bool
		if b.PG {
			if err := Admin.QueryRowContext(ctx, "SELECT COALESCE((SELECT state='active' AND query=$2 AND wait_event='ReplicationOriginDrop' FROM pg_stat_activity WHERE pid=$1),false)", pid, query).Scan(&observed); err != nil {
				t.Fatal("replication-origin DROP wait admission", err)
			}
		} else {
			observed, stacks = nativeOperationStack(stacks, symbols...)
		}
		if observed {
			select {
			case <-completed:
				t.Fatal("SQL DROP completed before replication-origin wait observation")
			default:
				return
			}
		}
		// A scheduling courtesy after an unsuccessful sample, never wait proof.
		if poll == nil {
			poll = time.NewTimer(time.Millisecond)
		} else {
			poll.Reset(time.Millisecond)
		}
		select {
		case <-completed:
			t.Fatal("SQL DROP completed before replication-origin wait admission")
		case <-ctx.Done():
			t.Fatalf("replication-origin DROP wait admission: %v\n%s", ctx.Err(), stacks)
		case <-poll.C:
		}
	}
}
