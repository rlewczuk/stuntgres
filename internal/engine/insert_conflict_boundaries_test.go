package engine

import (
	"context"

	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/sqltype"
)

type insertConflictInputCursor struct {
	schema                      []catalog.Column
	rows                        [][]sqltype.Value
	position, nextCalls, closes int
	nextError, closeError       error
}

func (c *insertConflictInputCursor) Schema() []catalog.Column { return c.schema }

func (c *insertConflictInputCursor) Next(ctx context.Context, out []sqltype.Value) (bool, error) {
	c.nextCalls++
	if err := ctx.Err(); err != nil {
		return false, err
	}
	if c.nextError != nil && c.position == 1 {
		return false, c.nextError
	}
	if c.position == len(c.rows) {
		return false, nil
	}
	copy(out, c.rows[c.position])
	c.position++
	return true, nil
}

func (c *insertConflictInputCursor) Close() error { c.closes++; return c.closeError }
