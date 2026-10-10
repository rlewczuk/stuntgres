package engine

import (
	"context"
	"math"
	"stuntgres/internal/sqltype"
)

// Shared comments follow principal identities, not database-local descriptors.
// The caller supplies the statement's saved RoleRead, including nested views.
func capturePGShDescription(ctx context.Context, roles RoleRead, ref ObjectRef, source *relationDescriptor) (*querySource, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	class, err := sqltype.OIDValue(source.columns[1].Usage.Type, 1260)
	if err != nil {
		return nil, err
	}
	rows := make([][]sqltype.Value, 0)
	for principal, role := range roles.state.roles {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		if role.Comment == "" || principal.id > math.MaxUint32 {
			continue
		}
		object, err := sqltype.OIDValue(source.columns[0].Usage.Type, uint32(principal.id))
		if err != nil {
			return nil, err
		}
		rows = append(rows, []sqltype.Value{object, class, sqltype.String(source.columns[2].Usage.Type, role.Comment)})
	}
	return &querySource{ref: ref, anonymous: rows}, nil
}
