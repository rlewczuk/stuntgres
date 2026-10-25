package engine

import (
	"context"
	"testing"
	"time"

	"stuntgres/internal/engine/lock"
)

func deadlockRequest(t *testing.T, e *Engine, ticket *lock.Ticket, resource lock.Resource, mode lock.Mode) *lock.Request {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	t.Cleanup(cancel)
	r, err := e.Locks.StartAcquire(ctx, ticket, resource, mode, lock.Policy{CompletionPurpose: lock.CompletionVirtual})
	if err != nil {
		t.Fatal(err)
	}
	return r
}

func deadlockResult(t *testing.T, r *lock.Request) error {
	t.Helper()
	if r == nil {
		return nil
	}
	if deadlockGuardCompleted(r.Done()) {
		return r.Err()
	}
	guard := time.NewTimer(time.Second)
	defer guard.Stop()
	if !deadlockAwait(r.Done(), guard.C) {
		t.Fatal("request did not terminate before failure guard")
	}
	return r.Err()
}

func deadlockAwait(done <-chan struct{}, guard <-chan time.Time) bool {
	if deadlockGuardCompleted(done) {
		return true
	}
	select {
	case <-done:
		return true
	case <-guard:
		return deadlockGuardCompleted(done)
	}
}

func deadlockGuardCompleted(done <-chan struct{}) bool {
	select {
	case <-done:
		return true
	default:
		return false
	}
}
