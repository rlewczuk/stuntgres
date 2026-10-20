package engine

import (
	"context"
	"errors"
	"fmt"
	"sync"
	"testing"
	"time"

	"stuntgres/internal/engine/ids"
	"stuntgres/internal/engine/lock"
)

// lockObservedDeadlineContext starts a real caller deadline only after the
// waiter has been observed. Admission and queue inspection must not consume
// the timeout being tested. Err comes from WithTimeout, not a relabeled cancel.
type lockObservedDeadlineContext struct {
	context.Context
	mu      sync.Mutex
	current context.Context
	cancel  context.CancelFunc
	done    chan struct{}
	once    sync.Once
}

func lockObservedDeadline(t *testing.T) *lockObservedDeadlineContext {
	t.Helper()
	current, cancel := context.WithCancel(context.Background())
	c := &lockObservedDeadlineContext{Context: context.Background(), current: current, cancel: cancel, done: make(chan struct{})}
	t.Cleanup(func() {
		c.mu.Lock()
		c.cancel()
		c.mu.Unlock()
		c.once.Do(func() { close(c.done) })
	})
	return c
}

func (c *lockObservedDeadlineContext) Done() <-chan struct{} { return c.done }

func (c *lockObservedDeadlineContext) Err() error {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.current.Err()
}

func (c *lockObservedDeadlineContext) Deadline() (time.Time, bool) {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.current.Deadline()
}

func (c *lockObservedDeadlineContext) arm(timeout time.Duration) {
	c.mu.Lock()
	c.cancel()
	c.current, c.cancel = context.WithTimeout(context.Background(), timeout)
	context.AfterFunc(c.current, func() { c.once.Do(func() { close(c.done) }) })
	c.mu.Unlock()
}

func lockLifecycleAsync(t *testing.T, e *Engine, s *Session, ctx context.Context, q string, prepare bool) <-chan error {
	t.Helper()
	ctx, cancel := context.WithCancel(ctx)
	done := lockAsync(e, s, ctx, q, prepare)
	// lockAsync's result channel is not closed. A separate completion channel
	// permits cleanup to join even after lockAwait has consumed the result.
	result := make(chan error, 1)
	joined := make(chan struct{})
	go func() {
		result <- <-done
		close(joined)
	}()
	t.Cleanup(func() {
		cancel()
		select {
		case <-joined:
		case <-time.After(5 * time.Second):
			t.Error("lifecycle lock worker did not join")
		}
	})
	return result
}

func lockObservedRequest(t *testing.T, e *Engine, s *Session, resource lock.Resource, mode lock.Mode) {
	t.Helper()
	state := e.locks.Inspect()
	entry := state.Resources[resource]
	if entry != nil {
		for _, request := range entry.Pending {
			if !request.Terminal && request.Mode == mode && request.Policy.Source != nil &&
				request.Ticket == state.Owners[request.Ticket.Ref()] &&
				request.Ticket.Ref().Session() == s.SessionToken {
				return
			}
		}
	}
	t.Fatal("exact owner/resource/mode request not queued", resource, mode)
}

func TestRelationLockWaitingDeadlineAndEnd(t *testing.T) {
	for _, q := range []string{"SELECT v FROM t", "INSERT INTO t VALUES(2)"} {
		for _, prepare := range []bool{false, true} {
			for _, explicit := range []bool{false, true} {
				t.Run(fmt.Sprintf("%s/%t/%t", q, prepare, explicit), func(t *testing.T) {
					e := New()
					a := sessionStart(t, e, nil)
					b := sessionStart(t, e, nil)
					sessionSQL(t, a, "CREATE TABLE t(v INT)")
					ra := transactionBegin(t, a)
					sessionSQL(t, a, "LOCK TABLE t IN ACCESS EXCLUSIVE MODE")
					var rb ids.TxRef
					if explicit {
						rb = transactionBegin(t, b)
					}
					ctx := lockObservedDeadline(t)
					done := lockLifecycleAsync(t, e, b, ctx, q, prepare)
					lockPending(t, e, 1)
					mode := lock.AccessShare
					if q == "INSERT INTO t VALUES(2)" {
						mode = lock.RowExclusive
					}
					lockObservedRequest(t, e, b, lock.Resource{ObjectRef: roleTableRef(t, e, "t")}, mode)
					ctx.arm(time.Second)
					lockAwait(t, done, context.DeadlineExceeded)
					lockPending(t, e, 0)
					if explicit {
						if b.TransactionState() != TxAborted {
							t.Fatal("not aborted")
						}
						transactionEnd(t, b, rb, false)
					} else if b.TransactionState() != TxIdle {
						t.Fatal("implicit did not recover")
					}
					transactionEnd(t, a, ra, false)
					transactionRows(t, b, "SELECT v FROM t")
					lockEmpty(t, e)
				})
			}
		}
	}
	t.Run("physical end active waiter", func(t *testing.T) {
		e := New()
		a := sessionStart(t, e, nil)
		b := sessionStart(t, e, nil)
		sessionSQL(t, a, "CREATE TABLE t(v INT)")
		ra := transactionBegin(t, a)
		sessionSQL(t, a, "LOCK TABLE t IN ACCESS EXCLUSIVE MODE")
		transactionBegin(t, b)
		done := lockLifecycleAsync(t, e, b, context.Background(), "SELECT v FROM t", false)
		lockPending(t, e, 1)
		lockObservedRequest(t, e, b, lock.Resource{ObjectRef: roleTableRef(t, e, "t")}, lock.AccessShare)
		b.RequestEnd(CleanClose)
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		if err := b.WaitEnd(ctx); err != nil {
			t.Fatal(err)
		}
		lockAwait(t, done, context.Canceled)
		lockPending(t, e, 0)
		transactionEnd(t, a, ra, false)
		lockEmpty(t, e)
	})
	t.Run("admitted failed commit", func(t *testing.T) {
		e := New()
		a := sessionStart(t, e, nil)
		b := sessionStart(t, e, nil)
		sessionSQL(t, a, "CREATE TABLE t(v INT)")
		ra := transactionBegin(t, a)
		sessionSQL(t, a, "LOCK TABLE t IN ACCESS EXCLUSIVE MODE")
		sessionSQL(t, a, "INSERT INTO t VALUES(1)")
		ctx, cancel := context.WithCancel(context.Background())
		op, err := a.Acquire(ctx)
		if err != nil {
			t.Fatal(err)
		}
		cancel()
		_, err = e.Commit(op, ra)
		op.Done()
		if !errors.Is(err, context.Canceled) {
			t.Fatal(err)
		}
		transactionRows(t, b, "SELECT v FROM t")
		lockEmpty(t, e)
	})
	t.Run("empty explicit birth and end", func(t *testing.T) {
		e := New()
		a := sessionStart(t, e, nil)
		for _, commit := range []bool{false, true} {
			ref := transactionBegin(t, a)
			transactionEnd(t, a, ref, commit)
			lockEmpty(t, e)
		}
	})
}
