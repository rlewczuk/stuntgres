package engine

import (
	"context"
	"sync"
	"testing"
	"time"

	"stuntgres/internal/engine/lock"
	"stuntgres/internal/engine/session"
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

func lockObservedRequest(t *testing.T, e *Engine, s *session.Session, resource lock.Resource, mode lock.Mode) {
	t.Helper()
	state := e.Locks.Inspect()
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
