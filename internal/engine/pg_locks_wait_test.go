package engine

import (
	"context"
	"testing"
	"time"

	"stuntgres/internal/engine/core"
	"stuntgres/internal/engine/lock"
)

func pgLocksUnitRequest(t *testing.T, e *core.Engine, ticket *lock.Ticket, resource lock.Resource, mode lock.Mode, purpose lock.CompletionPurpose) (*lock.Request, context.CancelFunc) {
	t.Helper()
	ctx, cancel := context.WithCancel(context.Background())
	r, err := e.Locks.StartAcquire(ctx, ticket, resource, mode, lock.Policy{CompletionPurpose: purpose})
	if err != nil {
		cancel()
		t.Fatal(err)
	}
	t.Cleanup(func() {
		cancel()
		if r != nil {
			select {
			case <-r.Done():
			case <-time.After(time.Second):
				t.Error("pending request did not join")
			}
		}
	})
	return r, cancel
}

func pgLocksUnitWait(t *testing.T, e *core.Engine, ticket *lock.Ticket) lock.Record {
	t.Helper()
	var waits []lock.Record
	for _, r := range pgLocksUnitCapture(t, e) {
		if r.Waiting && r.Owner.Session == ticket.Ref().Session().ID() {
			waits = append(waits, r)
		}
	}
	if len(waits) != 1 {
		t.Fatal("one real public wait", waits)
	}
	return waits[0]
}
