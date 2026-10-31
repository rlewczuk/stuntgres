package core_test

import (
	"context"
	"errors"
	"sync"
	"testing"
	"time"

	"stuntgres/internal/engine"
	"stuntgres/internal/engine/enginetest"
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
		e := engine.New()
		a, b := enginetest.StartSession(t, e, nil), enginetest.StartSession(t, e, nil)
		enginetest.SessionSQL(t, a, "CREATE TABLE t(v INT)")
		enginetest.SessionSQL(t, b, "SET lock_timeout='1h'")
		enginetest.SessionSQL(t, b, "SET statement_timeout='1h'")
		ra := enginetest.TransactionBegin(t, a)
		enginetest.SessionSQL(t, a, "LOCK TABLE t IN ACCESS EXCLUSIVE MODE")
		ctx, cancel := lockWaitCaller(t, deadline)
		done := waitsCommandAsync(e, b, ctx, "SELECT v FROM t", false)
		enginetest.LockPending(t, e, 1)
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
		enginetest.TransactionEnd(t, a, ra, false)
		enginetest.LockEmpty(t, e)
	}
}
