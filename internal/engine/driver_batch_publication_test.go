package engine

import (
	"context"
	"sync/atomic"

	"stuntgres/internal/engine/core"
	"stuntgres/internal/engine/session"
)

// Context.Err is the canonical cooperative publication seam. With no peer
// mutators, both mutexes are held only at the actual final live-operation check;
// this observes that boundary without depending on intermediate check counts.
type batchPublicationContext struct {
	context.Context
	e                *core.Engine
	s                *session.Session
	entered, release chan struct{}
	armed            atomic.Bool
	late             context.CancelFunc
}

func (c *batchPublicationContext) Err() error {
	if c.e.Mu.TryLock() {
		c.e.Mu.Unlock()
		return c.Context.Err()
	}
	if c.s.Mu.TryLock() {
		c.s.Mu.Unlock()
		return c.Context.Err()
	}
	if c.armed.CompareAndSwap(false, true) {
		close(c.entered)
		<-c.release
		if c.late != nil {
			c.late()
			return nil
		}
	}
	return c.Context.Err()
}
