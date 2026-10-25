package engine

import (
	"context"
)

type pgShDescriptionCancelContext struct {
	context.Context
	calls, cancelAt int
}

func (c *pgShDescriptionCancelContext) Err() error {
	c.calls++
	if c.calls >= c.cancelAt {
		return context.Canceled
	}
	return nil
}
