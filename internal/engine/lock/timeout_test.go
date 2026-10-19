package lock

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

func TestRelationLockWaitsTimeoutPerAcquisition(t *testing.T) {
	f := newLockFixture(t)
	waiter := f.tx()
	// A large real timer is only a failure guard. Explicit instants below,
	// rather than sleeps or timer delivery, determine the tested outcomes.
	policy := Policy{Timeout: time.Hour}
	wait := policy.Timeout * 3 / 4
	var firstDeadline time.Time
	for i := range 2 {
		holder, resource := f.tx(), f.relation()
		if err := f.m.Acquire(context.Background(), holder, resource, AccessExclusive); err != nil {
			t.Fatal(err)
		}
		before := time.Now()
		r, err := f.m.StartAcquire(context.Background(), waiter, resource, AccessShare, policy)
		after := time.Now()
		if err != nil || r == nil {
			t.Fatalf("acquisition %d did not queue: request=%v err=%v", i, r, err)
		}
		// Check the generated deadline before adjusting any test state. Reusing
		// the first acquisition's deadline or subtracting its consumed budget
		// fails this bound even though the same ticket and policy are reused.
		if r.deadline.Before(before.Add(policy.Timeout)) || r.deadline.After(after.Add(policy.Timeout)) {
			t.Fatalf("acquisition %d deadline %v is not a fresh timeout from [%v, %v]", i, r.deadline, before, after)
		}
		if i == 0 {
			// Age only the first request's private deadline, under the manager
			// mutex, as in the deadlock deadline tests. It still has a quarter
			// budget left at real release, but its start is now in the past.
			// Stop callback delivery so only the explicit instants are tested.
			f.m.mu.Lock()
			r.timer.Stop()
			r.deadline = r.deadline.Add(-wait)
			firstDeadline = r.deadline
			f.m.mu.Unlock()
		}
		start := r.deadline.Add(-policy.Timeout)
		at := start.Add(wait)
		if i == 1 && !at.After(firstDeadline) {
			t.Fatal("second wait did not cross the first acquisition's deadline")
		}
		if err := f.m.ExpireWait(r, at); err != nil || r.Terminal() {
			t.Fatalf("acquisition %d expired before its own deadline: %v", i, err)
		}
		f.m.End(holder)
		if err := f.result(r); err != nil {
			t.Fatalf("acquisition %d failed to transfer: %v", i, err)
		}
		if !r.Released() || f.m.Inspect().Resources[resource].Holders[waiter].Mode != AccessShare {
			t.Fatalf("acquisition %d did not retain a grant and retire its callbacks", i)
		}
	}

	// A fresh budget is not an unlimited budget: the same manager/request
	// path must still fail exactly at the next acquisition's own deadline.
	holder, resource := f.tx(), f.relation()
	if err := f.m.Acquire(context.Background(), holder, resource, AccessExclusive); err != nil {
		t.Fatal(err)
	}
	r, err := f.m.StartAcquire(context.Background(), waiter, resource, AccessShare, policy)
	if err != nil || r == nil {
		t.Fatal("deadline-boundary request did not queue", err)
	}
	f.diagnostic(f.m.ExpireWait(r, r.deadline), "55P03", "canceling statement due to lock timeout")
	f.diagnostic(f.result(r), "55P03", "canceling statement due to lock timeout")
	if !r.Released() {
		t.Fatal("expired request retained a timer or cancellation callback")
	}
	f.m.End(holder)
	f.m.End(waiter)
	f.empty()
}

func TestRelationLockWaitsDelayedDeadlines(t *testing.T) {
	f := newLockFixture(t)
	at, bt := f.tx(), f.tx()
	resource := f.relation()
	if err := f.m.Acquire(context.Background(), at, resource, AccessExclusive); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	policy := Policy{Timeout: time.Hour, StatementDeadline: time.Now().Add(2 * time.Hour)}
	r, err := f.m.StartAcquire(ctx, bt, resource, AccessShare, policy)
	if err != nil || r == nil {
		t.Fatal(err)
	}
	// Keep the real callbacks in the future, then model both deadlines having
	// passed while metadata synchronization delayed their delivery. As in the
	// deadlock deadline tests, install logical past instants under the mutex.
	// Release must arbitrate the expired lock deadline before transferring.
	f.m.mu.Lock()
	expired := time.Now().Add(-time.Hour)
	r.deadline = expired
	r.policy.StatementDeadline = expired.Add(time.Minute)
	var notify *relationRequest
	f.m.endLocked(at, ErrOwnerEnded, &notify)
	f.m.mu.Unlock()
	notifyRequests(notify)
	f.diagnostic(r.Wait(), "55P03", "canceling statement due to lock timeout")
	f.m.End(bt)
	f.empty()
}
