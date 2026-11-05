package core

import (
	"context"

	"stuntgres/internal/engine/expr"
	"stuntgres/internal/sqltype"
)

func (h *contribHost) PgBuffercacheNUMA(result *sqltype.Descriptor) (expr.SourceCursor, error) {
	host := h.host()
	if err := host.Active(); err != nil {
		return expr.SourceCursor{}, err
	}
	e := host.scope().Engine()
	e.Mu.Lock()
	capacity, err := e.logicalBufferNUMACapacityLocked()
	e.Mu.Unlock()
	if err != nil {
		return expr.SourceCursor{}, err
	}
	// Only detached scalars and the resident result descriptor leave the lock.
	// No occupancy snapshot, source lease, engine or session is retained.
	return expr.NewSourceCursor(expr.SourceCursor{Type: result, Stop: capacity, Advance: nextLogicalBufferNUMA}), nil
}

func (e *Engine) logicalBufferNUMACapacityLocked() (int64, error) {
	if err := e.registeredLogicalBufferOwnerLocked(); err != nil {
		return 0, err
	}
	return e.logicalBuffersLocked().capacity, nil
}

func nextLogicalBufferNUMA(ctx context.Context, cursor *expr.SourceCursor) (sqltype.Value, bool, error) {
	if err := ctx.Err(); err != nil {
		return sqltype.Value{}, false, err
	}
	id := int64(cursor.Position) + 1
	if id > cursor.Stop {
		return sqltype.Value{}, false, nil
	}
	cursor.Position++
	// The virtual OS page is one cache buffer in a supported single-node
	// topology. These are technical indices, never host addresses or syscalls.
	values := [...]int64{id, id - 1, 0}
	fields := make([]sqltype.Value, len(values))
	for i, value := range values {
		var err error
		fields[i], err = sqltype.Integer(cursor.Type.CompositeMember(i).Usage.Type, value)
		if err != nil {
			return sqltype.Value{}, false, err
		}
	}
	value, err := sqltype.OwnedCompositeValue(cursor.Type, fields)
	return value, err == nil, err
}
