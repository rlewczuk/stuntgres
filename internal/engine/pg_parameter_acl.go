package engine

import (
	"context"

	"stuntgres/internal/engine/access"
	"stuntgres/internal/engine/authorization"
	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/sqltype"
)

// Parameter identities and sparse membership belong to the grant owner. Read
// only actual descriptors, using the statement's matching catalog/role roots.
func (e *Engine) capturePGParameterACL(ctx context.Context, view catalog.Read, roles authorization.Read, ref ids.ObjectRef, source *catalog.Relation) (*querySource, error) {
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
		if err := (parameterProvider{&e.settings}).Validate(view, d); err != nil {
			return nil, err
		}
		if err := access.ValidateACL(roles, d); err != nil {
			return nil, err
		}
		id, err := sqltype.OIDValue(source.Columns[0].Usage.Type, uint32(d.Address().ObjectID))
		if err != nil {
			return nil, err
		}
		acl, err := e.pgClassACL(roles, source.Columns[2].Usage.Type, d.ACL)
		if err != nil {
			return nil, err
		}
		rows = append(rows, []sqltype.Value{id, sqltype.String(source.Columns[1].Usage.Type, d.Name()), acl})
	}
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	return &querySource{ref: ref, anonymous: rows}, nil
}
