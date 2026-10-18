package engine

import (
	"context"
	"fmt"

	"stuntgres/internal/engine/authorization"
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/sqltype"
)

// Capture descriptors and ACLs from the same immutable root as the query's
// other catalog sources. Later grants cannot mutate already captured rows.
func (e *Engine) capturePGTablespace(ctx context.Context, catalog CatalogRead, roles authorization.Read, ref ids.ObjectRef, source *relationDescriptor) (*querySource, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	rows := make([][]sqltype.Value, len(builtinTablespaces))
	for i, builtin := range builtinTablespaces {
		d, ok := catalog.state.descriptors[ids.ObjectAddress{ClassID: TablespaceClassID, ObjectID: builtin.oid}].(*tablespaceDescriptor)
		if !ok || d == nil {
			return nil, fmt.Errorf("stuntgres: missing builtin tablespace descriptor")
		}
		if err := validateACL(roles, d); err != nil {
			return nil, err
		}
		id, err := sqltype.OIDValue(source.columns[0].Usage.Type, uint32(d.address.ObjectID))
		if err != nil {
			return nil, err
		}
		owner, err := sqltype.OIDValue(source.columns[2].Usage.Type, uint32(d.owner.ID()))
		if err != nil {
			return nil, err
		}
		acl, err := e.pgClassACL(roles, source.columns[3].Usage.Type, d.acl)
		if err != nil {
			return nil, err
		}
		rows[i] = []sqltype.Value{id, sqltype.String(source.columns[1].Usage.Type, d.name), owner, acl, sqltype.Null(source.columns[4].Usage.Type)}
	}
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	return &querySource{ref: ref, anonymous: rows}, nil
}
