package engine

import (
	"context"
	"math"
	"stuntgres/internal/engine/authorization"
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/sqltype"
)

// Project the statement's immutable role root. All role attributes are direct;
// neither membership edges nor credential material participate in this view.
func capturePGRoles(ctx context.Context, roles authorization.Read, ref ids.ObjectRef, source *relationDescriptor) (*querySource, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	rows := make([][]sqltype.Value, 0, roles.RoleCount())
	for principal, role := range roles.RoleEntries() {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		if principal.ID() > math.MaxUint32 {
			continue
		}
		id, err := sqltype.OIDValue(source.columns[12].Usage.Type, uint32(principal.ID()))
		if err != nil {
			return nil, err
		}
		expiry := sqltype.Null(source.columns[9].Usage.Type)
		if role.HasValidUntil {
			expiry, err = sqltype.TimestampValue(source.columns[9].Usage.Type, role.ValidUntil)
			if err != nil {
				return nil, err
			}
		}
		limit, err := sqltype.Integer(source.columns[7].Usage.Type, int64(role.ConnectionLimit))
		if err != nil {
			return nil, err
		}
		rows = append(rows, []sqltype.Value{
			sqltype.String(source.columns[0].Usage.Type, role.Name),
			sqltype.Boolean(source.columns[1].Usage.Type, role.Superuser),
			sqltype.Boolean(source.columns[2].Usage.Type, role.Inherit),
			sqltype.Boolean(source.columns[3].Usage.Type, role.CreateRole),
			sqltype.Boolean(source.columns[4].Usage.Type, role.CreateDB),
			sqltype.Boolean(source.columns[5].Usage.Type, role.Login),
			sqltype.Boolean(source.columns[6].Usage.Type, role.Replication),
			limit,
			sqltype.String(source.columns[8].Usage.Type, "********"), expiry,
			sqltype.Boolean(source.columns[10].Usage.Type, role.BypassRLS),
			sqltype.Null(source.columns[11].Usage.Type), id,
		})
	}
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	return &querySource{ref: ref, anonymous: rows}, nil
}
