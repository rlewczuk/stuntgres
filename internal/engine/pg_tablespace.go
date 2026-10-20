package engine

import (
	"fmt"

	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/sqltype"
)

// Capture descriptors and ACLs from the same immutable root as the query's
// other catalog sources. Later grants cannot mutate already captured rows.
func capturePGTablespace(in Input) ([][]sqltype.Value, error) {
	ctx := in.Context
	view := in.Catalog
	roles := in.Roles
	source := in.Source
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	rows := make([][]sqltype.Value, len(catalog.BuiltinTablespaces))
	for i, builtin := range catalog.BuiltinTablespaces {
		d, ok := view.Tablespace(ids.ObjectAddress{ClassID: ids.TablespaceClassID, ObjectID: builtin.OID})
		if !ok || d == nil {
			return nil, fmt.Errorf("stuntgres: missing builtin tablespace descriptor")
		}
		if err := in.Runtime.ValidateACL(roles, d); err != nil {
			return nil, err
		}
		id, err := sqltype.OIDValue(source.Columns[0].Usage.Type, uint32(d.Address().ObjectID))
		if err != nil {
			return nil, err
		}
		owner, err := sqltype.OIDValue(source.Columns[2].Usage.Type, uint32(d.Owner.ID()))
		if err != nil {
			return nil, err
		}
		acl, err := ClassACL(in.Owner, roles, source.Columns[3].Usage.Type, d.ACL)
		if err != nil {
			return nil, err
		}
		rows[i] = []sqltype.Value{id, sqltype.String(source.Columns[1].Usage.Type, d.Name()), owner, acl, sqltype.Null(source.Columns[4].Usage.Type)}
	}
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	return rows, nil
}
