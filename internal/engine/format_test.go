package engine

import (
	"context"
	"stuntgres/internal/sqltype"
)

func formatUnitTypes() map[sqltype.ID]*sqltype.Descriptor {
	types := make(map[sqltype.ID]*sqltype.Descriptor)
	for _, descriptor := range sqltype.Builtins() {
		types[descriptor.ID()] = descriptor
	}
	return types
}

type formatWorkContext struct {
	context.Context
	checks int
}

func (c *formatWorkContext) Err() error {
	c.checks--
	if c.checks <= 0 {
		return context.Canceled
	}
	return nil
}
