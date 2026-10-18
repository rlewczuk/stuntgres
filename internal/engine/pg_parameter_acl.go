package engine

import (
	"context"

	"stuntgres/internal/engine/authorization"
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/sqltype"
)

// Parameter identities and sparse membership belong to the grant owner. Read
// only actual descriptors, using the statement's matching catalog/role roots.
func (e *Engine) capturePGParameterACL(ctx context.Context, catalog CatalogRead, roles authorization.Read, ref ids.ObjectRef, source *relationDescriptor) (*querySource, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	rows := make([][]sqltype.Value, 0)
	for _, descriptor := range catalog.Descriptors() {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		d, ok := descriptor.(*parameterDescriptor)
		if !ok {
			continue
		}
		if err := (parameterProvider{e.settingsDefinitions}).Validate(catalog, d); err != nil {
			return nil, err
		}
		if err := validateACL(roles, d); err != nil {
			return nil, err
		}
		id, err := sqltype.OIDValue(source.Columns[0].Usage.Type, uint32(d.address.ObjectID))
		if err != nil {
			return nil, err
		}
		acl, err := e.pgClassACL(roles, source.Columns[2].Usage.Type, d.ACL)
		if err != nil {
			return nil, err
		}
		rows = append(rows, []sqltype.Value{id, sqltype.String(source.Columns[1].Usage.Type, d.name), acl})
	}
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	return &querySource{ref: ref, anonymous: rows}, nil
}
