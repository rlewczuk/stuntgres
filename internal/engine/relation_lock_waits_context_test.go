package engine

import (
	"context"
	"errors"
	"testing"
	"time"

	"stuntgres/internal/engine/lock"
	"stuntgres/internal/sqlstate"
)

func TestRelationLockWaitsCallerContext(t *testing.T) {
	for _, deadline := range []bool{false, true} {
		e := New()
		a, b := sessionStart(t, e, nil), sessionStart(t, e, nil)
		sessionSQL(t, a, "CREATE TABLE t(v INT)")
		sessionSQL(t, b, "SET lock_timeout='1s'")
		sessionSQL(t, b, "SET statement_timeout='1s'")
		ra := transactionBegin(t, a)
		sessionSQL(t, a, "LOCK TABLE t IN ACCESS EXCLUSIVE MODE")
		ctx, cancel := context.WithTimeout(context.Background(), 80*time.Millisecond)
		if !deadline {
			cancel()
			ctx, cancel = context.WithCancel(context.Background())
		}
		done := waitsCommandAsync(e, b, ctx, "SELECT v FROM t", false)
		lockPending(t, e, 1)
		if !deadline {
			cancel()
		}
		select {
		case err := <-done:
			want := error(context.Canceled)
			if deadline {
				want = context.DeadlineExceeded
			}
			var diagnostic *sqlstate.Error
			if !errors.Is(err, want) || errors.As(err, &diagnostic) {
				t.Fatal("fabricated context diagnostic", err)
			}
		case <-time.After(5 * time.Second):
			t.Fatal("caller interruption did not finish")
		}
		cancel()
		transactionEnd(t, a, ra, false)
		lockEmpty(t, e)
	}
}

func TestRelationLockWaitsUntimedGrant(t *testing.T) {
	e := New()
	a, b := sessionStart(t, e, nil), sessionStart(t, e, nil)
	sessionSQL(t, a, "CREATE TABLE t(v INT)")
	ra, rb := transactionBegin(t, a), transactionBegin(t, b)
	ref := catalogRef(t, e, e.CatalogRead(), "t")
	policy := lock.Policy{Timeout: 20 * time.Millisecond}
	if r, err := e.locks.StartAcquire(context.Background(), a.transactions.ticket, lock.Resource{ObjectRef: ref}, lock.AccessExclusive, policy); r != nil || err != nil {
		t.Fatal(r, err)
	}
	time.Sleep(40 * time.Millisecond)
	if r, err := e.locks.StartAcquire(context.Background(), a.transactions.ticket, lock.Resource{ObjectRef: ref}, lock.AccessShare, policy); r != nil || err != nil {
		t.Fatal("immediate grant retained timer", r, err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	r, err := e.locks.StartAcquire(ctx, b.transactions.ticket, lock.Resource{ObjectRef: ref}, lock.AccessShare, lock.Policy{})
	if r == nil || err != nil {
		t.Fatal(r, err)
	}
	time.Sleep(40 * time.Millisecond)
	// The manager arms a lock timer exactly when the request has a deadline.
	if r.Terminal() || !r.Deadline().IsZero() {
		t.Fatal("zero timeout was armed")
	}
	transactionEnd(t, a, ra, false)
	if err = r.Wait(); err != nil {
		t.Fatal(err)
	}
	time.Sleep(40 * time.Millisecond)
	if repeated, err := e.locks.StartAcquire(context.Background(), b.transactions.ticket, lock.Resource{ObjectRef: ref}, lock.AccessShare, policy); repeated != nil || err != nil {
		t.Fatal(repeated, err)
	}
	transactionEnd(t, b, rb, false)
	lockEmpty(t, e)
}
