package engine

import (
	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/sqltype"
)

// Parameter identities and sparse membership belong to the grant owner. Read
// only actual descriptors, using the statement's matching catalog/role roots.
func capturePGParameterACL(in Input) ([][]sqltype.Value, error) {
	ctx := in.Context
	view := in.Catalog
	roles := in.Roles
	source := in.Source
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	rows := make([][]sqltype.Value, 0)
	for _, descriptor := range view.Descriptors() {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		d, ok := descriptor.(*catalog.Parameter)
		if !ok {
			continue
		}
		if err := in.Runtime.ValidateParameter(view, d); err != nil {
			return nil, err
		}
		if err := in.Runtime.ValidateACL(roles, d); err != nil {
			return nil, err
		}
		id, err := sqltype.OIDValue(source.Columns[0].Usage.Type, uint32(d.Address().ObjectID))
		if err != nil {
			return nil, err
		}
		acl, err := ClassACL(in.Owner, roles, source.Columns[2].Usage.Type, d.ACL)
		if err != nil {
			return nil, err
		}
		rows = append(rows, []sqltype.Value{id, sqltype.String(source.Columns[1].Usage.Type, d.Name()), acl})
	}
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	return rows, nil
}
