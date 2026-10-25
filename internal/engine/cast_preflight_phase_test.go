package engine

import (
	"context"
	"testing"

	"stuntgres/internal/engine/core"
)

// The importer arms this context after analysis/import. The first following
// cancellation checkpoint is preflight entry. TryLock observes lock ownership
// deterministically, without sleeps, goroutines, or a production test hook.
type castPreflightContext struct {
	context.Context
	e        *core.Engine
	t        *testing.T
	armed    bool
	entered  bool
	cancelAt string
	visit    func()
}

func (c *castPreflightContext) Err() error {
	if !c.armed {
		return c.Context.Err()
	}
	if !c.e.Mu.TryLock() {
		if !c.entered {
			c.t.Error("SELECT preflight entered with Engine.mu held")
			return context.Canceled
		}
		if c.cancelAt == "relock" {
			return context.Canceled
		}
		return c.Context.Err()
	}
	defer c.e.Mu.Unlock()
	if !c.entered {
		c.entered = true
		c.visit()
	}
	if c.cancelAt == "preflight" {
		return context.Canceled
	}
	return c.Context.Err()
}
