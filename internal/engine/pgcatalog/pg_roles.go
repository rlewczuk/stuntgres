package pgcatalog

import (
	"math"
	"stuntgres/internal/sqltype"
)

// Project the statement's immutable role root. All role attributes are direct;
// neither membership edges nor credential material participate in this view.
func capturePGRoles(in Input) ([][]sqltype.Value, error) {
	ctx := in.Context
	roles := in.Roles
	source := in.Source
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
		id, err := sqltype.OIDValue(source.Columns[12].Usage.Type, uint32(principal.ID()))
		if err != nil {
			return nil, err
		}
		expiry := sqltype.Null(source.Columns[9].Usage.Type)
		if role.HasValidUntil {
			expiry, err = sqltype.TimestampValue(source.Columns[9].Usage.Type, role.ValidUntil)
			if err != nil {
				return nil, err
			}
		}
		limit, err := sqltype.Integer(source.Columns[7].Usage.Type, int64(role.ConnectionLimit))
		if err != nil {
			return nil, err
		}
		rows = append(rows, []sqltype.Value{
			sqltype.String(source.Columns[0].Usage.Type, role.Name),
			sqltype.Boolean(source.Columns[1].Usage.Type, role.Superuser),
			sqltype.Boolean(source.Columns[2].Usage.Type, role.Inherit),
			sqltype.Boolean(source.Columns[3].Usage.Type, role.CreateRole),
			sqltype.Boolean(source.Columns[4].Usage.Type, role.CreateDB),
			sqltype.Boolean(source.Columns[5].Usage.Type, role.Login),
			sqltype.Boolean(source.Columns[6].Usage.Type, role.Replication),
			limit,
			sqltype.String(source.Columns[8].Usage.Type, "********"), expiry,
			sqltype.Boolean(source.Columns[10].Usage.Type, role.BypassRLS),
			sqltype.Null(source.Columns[11].Usage.Type), id,
		})
	}
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	return rows, nil
}
