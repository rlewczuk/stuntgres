package engine

import (
	"context"
	"errors"
	"testing"
	"time"

	"stuntgres/internal/sqlstate"
)

// diagnostic fails unless err is exactly the ERROR with code and message.
func (f *lockFixture) diagnostic(err error, code, message string) {
	f.t.Helper()
	var got *sqlstate.Error
	want := sqlstate.Error{Severity: "ERROR", SeverityUnlocalized: "ERROR", Code: code, Message: message}
	if !errors.As(err, &got) || *got != want {
		f.t.Fatalf("diagnostic = %#v (%v), want %#v", got, err, want)
	}
}

func TestRelationLockWaitsDelayedDeadlines(t *testing.T) {
	f := newLockFixture(t)
	at, bt := f.tx(), f.tx()
	resource := f.relation()
	if err := f.m.Acquire(context.Background(), at, resource, accessExclusive); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	policy := relationLockPolicy{Timeout: 40 * time.Millisecond, StatementDeadline: time.Now().Add(80 * time.Millisecond)}
	r, err := f.m.StartAcquire(ctx, bt, resource, accessShare, policy)
	if err != nil || r == nil {
		t.Fatal(err)
	}
	// Hold the manager mutex so neither deadline callback can run, and let wall
	// time pass both absolute deadlines. The mutex, not timing, orders the
	// callbacks after the release below.
	f.m.mu.Lock()
	time.Sleep(time.Until(policy.StatementDeadline) + time.Millisecond)
	// Simulate a release transition while both callbacks are delayed by
	// metadata synchronization. Expired lock deadline must beat transfer.
	var notify *relationRequest
	f.m.endLocked(at, errRelationOwnerEnded, &notify)
	f.m.mu.Unlock()
	notifyRequests(notify)
	f.diagnostic(r.Wait(), "55P03", "canceling statement due to lock timeout")
	f.m.End(bt)
	f.empty()
}
