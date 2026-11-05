package core

import (
	"context"
	"fmt"
	"stuntgres/internal/engine/expr"
	"stuntgres/internal/sqltype"
)

// Only detached scalar occupancy leaves Engine.Mu. Iteration synthesizes unused
// slots lazily, with no retained scope, catalog root, engine or source lease.
type logicalBufferSnapshot struct {
	capacity int64
	rows     map[int64]logicalBufferRow
}

func (h *contribHost) PgBuffercache(result *sqltype.Descriptor) (expr.SourceCursor, error) {
	host := h.host()
	if err := host.Active(); err != nil {
		return expr.SourceCursor{}, err
	}
	e := host.scope().Engine()
	e.Mu.Lock()
	snapshot, err := e.snapshotLogicalBuffersLocked()
	e.Mu.Unlock()
	if err != nil {
		return expr.SourceCursor{}, err
	}
	return expr.NewSourceCursor(expr.SourceCursor{Type: result, State: &snapshot, Advance: nextLogicalBuffer}), nil
}

// Unlike private publication, an observation needs a genuine live cache owner.
// Never fabricate empty rows or a private cache for an unattached runtime.
func (e *Engine) snapshotLogicalBuffersLocked() (logicalBufferSnapshot, error) {
	if err := e.registeredLogicalBufferOwnerLocked(); err != nil {
		return logicalBufferSnapshot{}, err
	}
	e.reconcileLogicalBuffersLocked()
	capacity, rows := e.logicalBuffersLocked().snapshot()
	return logicalBufferSnapshot{capacity: capacity, rows: rows}, nil
}
func (e *Engine) registeredLogicalBufferOwnerLocked() error {
	cluster := e.Cluster
	if cluster == nil || e.Mu != &cluster.Mu || e.RoleStore != &cluster.Roles {
		return fmt.Errorf("stuntgres: pg_buffercache requires a registered database runtime")
	}
	database, ok := e.Catalog.Current().Database(e.Catalog.DatabaseAddress())
	if !ok || cluster.Targets[database.Name()] != e {
		return fmt.Errorf("stuntgres: pg_buffercache requires a registered database runtime")
	}
	return nil
}
func nextLogicalBuffer(ctx context.Context, cursor *expr.SourceCursor) (sqltype.Value, bool, error) {
	if err := ctx.Err(); err != nil {
		return sqltype.Value{}, false, err
	}
	snapshot := cursor.State.(*logicalBufferSnapshot)
	id := int64(cursor.Position) + 1
	if id > snapshot.capacity {
		return sqltype.Value{}, false, nil
	}
	cursor.Position++
	shape := cursor.Type
	fields := make([]sqltype.Value, shape.CompositeLen())
	for i := range fields {
		fields[i] = sqltype.Null(shape.CompositeMember(i).Usage.Type)
	}
	var err error
	fields[0], err = sqltype.Integer(shape.CompositeMember(0).Usage.Type, id)
	if err != nil {
		return sqltype.Value{}, false, err
	}
	if row, occupied := snapshot.rows[id]; occupied {
		identities := [...]uint32{uint32(row.node), uint32(row.space), uint32(row.database)}
		for i, value := range identities {
			fields[i+1], err = sqltype.OIDValue(shape.CompositeMember(i+1).Usage.Type, value)
			if err != nil {
				return sqltype.Value{}, false, err
			}
		}
		for _, i := range [...]int{4, 5, 7, 8} {
			if i == len(fields) {
				continue
			}
			value := int64(0)
			if i == 7 {
				value = row.usage
			}
			if i == 8 {
				value = row.pins
			}
			fields[i], err = sqltype.Integer(shape.CompositeMember(i).Usage.Type, value)
			if err != nil {
				return sqltype.Value{}, false, err
			}
		}
		fields[6] = sqltype.Boolean(shape.CompositeMember(6).Usage.Type, false)
	}
	value, err := sqltype.OwnedCompositeValue(shape, fields)
	return value, err == nil, err
}
