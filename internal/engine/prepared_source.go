package engine

import (
	"context"
	"stuntgres/internal/sqltype"
)

// Engine.mu protects the short registry snapshot. Rows own scalar datums and
// never retain entries or session attachments.
func (e *Engine) capturePreparedSourceLocked(ctx context.Context, ref ObjectRef, source *relationDescriptor) (*querySource, error) {
	rows := make([][]sqltype.Value, 0, len(e.prepared))
	for _, entry := range e.prepared {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		xid, err := sqltype.XIDValue(source.columns[0].Usage.Type, entry.ref.diagnostic)
		if err != nil {
			return nil, err
		}
		prepared, err := sqltype.TimestampFromTime(source.columns[2].Usage.Type, entry.prepared)
		if err != nil {
			return nil, err
		}
		owner := sqltype.Null(source.columns[3].Usage.Type)
		if role, ok := e.roles.roles[entry.owner]; ok {
			owner = sqltype.String(source.columns[3].Usage.Type, role.Name)
		}
		rows = append(rows, []sqltype.Value{xid, sqltype.String(source.columns[1].Usage.Type, entry.gid), prepared, owner, sqltype.String(source.columns[4].Usage.Type, entry.database)})
	}
	return &querySource{ref: ref, anonymous: rows}, nil
}

// Statement admission serializes mutation of the sticky top-level footprint.
// Savepoint restoration deliberately never restores it.
func (e *Engine) markTemporaryAccessLocked(scope *StatementScope, ref ObjectRef) {
	catalog := e.privateCatalogLocked(scope.owner.catalog)
	if scope.catalog != nil {
		catalog = e.privateCatalogLocked(scope.catalog)
	}
	d, ok := catalog.state.descriptors[ref.Address].(*relationDescriptor)
	if !ok {
		return
	}
	if ns, ok := catalog.state.descriptors[d.namespace].(*namespaceDescriptor); ok && ns.temporary == scope.op.session.token {
		scope.owner.accessedTemporary = true
	}
}
