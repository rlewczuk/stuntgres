package engine

import (
	"context"
	"stuntgres/internal/engine/authorization"
	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/sqltype"
)

// Engine.mu protects the short registry snapshot. Rows own scalar datums and
// never retain entries or session attachments.
func (e *Engine) capturePreparedSourceLocked(ctx context.Context, ref ids.ObjectRef, source *catalog.Relation) (*querySource, error) {
	rows := make([][]sqltype.Value, 0, len(e.prepared))
	for _, entry := range e.prepared {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		xid, err := sqltype.XIDValue(source.Columns[0].Usage.Type, e.visibility[entry.ref].assignedXID)
		if err != nil {
			return nil, err
		}
		prepared, err := sqltype.TimestampFromTime(source.Columns[2].Usage.Type, entry.prepared)
		if err != nil {
			return nil, err
		}
		owner := sqltype.Null(source.Columns[3].Usage.Type)
		if role, ok := e.roles.published.root.Read(authorization.PrincipalRef{}).Find(entry.owner); ok {
			owner = sqltype.String(source.Columns[3].Usage.Type, role.Name)
		}
		rows = append(rows, []sqltype.Value{xid, sqltype.String(source.Columns[1].Usage.Type, entry.gid), prepared, owner, sqltype.String(source.Columns[4].Usage.Type, entry.database)})
	}
	return &querySource{ref: ref, anonymous: rows}, nil
}

// Statement admission serializes mutation of the sticky top-level footprint.
// Savepoint restoration deliberately never restores it.
func (e *Engine) markTemporaryAccessLocked(scope *StatementScope, ref ids.ObjectRef) {
	catalog := e.privateCatalogLocked(scope.owner.catalog)
	if scope.catalog != nil {
		catalog = e.privateCatalogLocked(scope.catalog)
	}
	d, ok := catalog.Relation(ref.Address)
	if !ok {
		return
	}
	if ns, ok := catalog.Namespace(d.Namespace()); ok && ns.Temporary == scope.op.session.token {
		scope.owner.accessedTemporary = true
	}
}
