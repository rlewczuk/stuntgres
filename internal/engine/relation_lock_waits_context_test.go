package engine

import (
	"context"
	"errors"
	"sync"
	"testing"
	"time"

	"stuntgres/internal/engine/lock"
	"stuntgres/internal/sqlstate"
)

// lockWaitCallerContext gives tests control of the caller's terminal event.
// No wall-clock deadline races the observer: expire is called only after the
// genuine lock request queues. The deadline case retains the ordinary Go
// DeadlineExceeded identity rather than substituting a SQL timeout.
type lockWaitCallerContext struct {
	context.Context
	done chan struct{}
	once sync.Once
	err  error
}

func (c *lockWaitCallerContext) Done() <-chan struct{} { return c.done }
func (c *lockWaitCallerContext) Err() error {
	select {
	case <-c.done:
		return c.err
	default:
		return nil
	}
}

func lockWaitCaller(t *testing.T, deadline bool) (context.Context, func()) {
	t.Helper()
	err := error(context.Canceled)
	if deadline {
		err = context.DeadlineExceeded
	}
	ctx := &lockWaitCallerContext{Context: context.Background(), done: make(chan struct{}), err: err}
	expire := func() { ctx.once.Do(func() { close(ctx.done) }) }
	t.Cleanup(expire)
	return ctx, expire
}

func TestRelationLockWaitsCallerContext(t *testing.T) {
	for _, deadline := range []bool{false, true} {
		e := New()
		a, b := sessionStart(t, e, nil), sessionStart(t, e, nil)
		sessionSQL(t, a, "CREATE TABLE t(v INT)")
		sessionSQL(t, b, "SET lock_timeout='1h'")
		sessionSQL(t, b, "SET statement_timeout='1h'")
		ra := transactionBegin(t, a)
		sessionSQL(t, a, "LOCK TABLE t IN ACCESS EXCLUSIVE MODE")
		ctx, cancel := lockWaitCaller(t, deadline)
		done := waitsCommandAsync(e, b, ctx, "SELECT v FROM t", false)
		lockPending(t, e, 1)
		cancel()
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
	if r, err := e.locks.StartAcquire(context.Background(), a.Transactions.Ticket, lock.Resource{ObjectRef: ref}, lock.AccessExclusive, policy); r != nil || err != nil {
		t.Fatal(r, err)
	}
	if r, err := e.locks.StartAcquire(context.Background(), a.Transactions.Ticket, lock.Resource{ObjectRef: ref}, lock.AccessShare, policy); r != nil || err != nil {
		t.Fatal("immediate grant retained timer", r, err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	r, err := e.locks.StartAcquire(ctx, b.Transactions.Ticket, lock.Resource{ObjectRef: ref}, lock.AccessShare, lock.Policy{})
	if r == nil || err != nil {
		t.Fatal(r, err)
	}
	at := time.Now().Add(time.Hour)
	if err := e.locks.ExpireWait(r, at); err != nil {
		t.Fatal("untimed request expired", err)
	}
	// The manager arms a lock timer exactly when the request has a deadline.
	if r.Terminal() || !r.Deadline().IsZero() {
		t.Fatal("zero timeout was armed")
	}
	transactionEnd(t, a, ra, false)
	if err = r.Wait(); err != nil {
		t.Fatal(err)
	}
	if err := e.locks.ExpireWait(r, at.Add(time.Hour)); err != nil || !r.Terminal() {
		t.Fatal("grant changed after later expiration", err)
	}
	if repeated, err := e.locks.StartAcquire(context.Background(), b.Transactions.Ticket, lock.Resource{ObjectRef: ref}, lock.AccessShare, policy); repeated != nil || err != nil {
		t.Fatal(repeated, err)
	}
	transactionEnd(t, b, rb, false)
	lockEmpty(t, e)
}
