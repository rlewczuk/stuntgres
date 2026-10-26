package pgcatalog

import (
	"math"

	"stuntgres/internal/sqltype"
)

// Project all roles from the statement's immutable root. Admission belongs to
// the common relation/column ACL path, not to this trusted row producer.
func capturePGAuthID(in Input) ([][]sqltype.Value, error) {
	if err := in.Context.Err(); err != nil {
		return nil, err
	}
	columns := in.Source.Columns
	rows := make([][]sqltype.Value, 0, in.Roles.RoleCount())
	for principal, role := range in.Roles.RoleEntries() {
		if err := in.Context.Err(); err != nil {
			return nil, err
		}
		if principal.ID() > math.MaxUint32 {
			continue
		}
		id, err := sqltype.OIDValue(columns[0].Usage.Type, uint32(principal.ID()))
		if err != nil {
			return nil, err
		}
		limit, err := sqltype.Integer(columns[9].Usage.Type, int64(role.ConnectionLimit))
		if err != nil {
			return nil, err
		}
		password := sqltype.Null(columns[10].Usage.Type)
		if role.Password != "" {
			password = sqltype.String(columns[10].Usage.Type, role.Password)
		}
		expiry := sqltype.Null(columns[11].Usage.Type)
		if role.HasValidUntil {
			expiry, err = sqltype.TimestampValue(columns[11].Usage.Type, role.ValidUntil)
			if err != nil {
				return nil, err
			}
		}
		rows = append(rows, []sqltype.Value{
			id, sqltype.String(columns[1].Usage.Type, role.Name),
			sqltype.Boolean(columns[2].Usage.Type, role.Superuser),
			sqltype.Boolean(columns[3].Usage.Type, role.Inherit),
			sqltype.Boolean(columns[4].Usage.Type, role.CreateRole),
			sqltype.Boolean(columns[5].Usage.Type, role.CreateDB),
			sqltype.Boolean(columns[6].Usage.Type, role.Login),
			sqltype.Boolean(columns[7].Usage.Type, role.Replication),
			sqltype.Boolean(columns[8].Usage.Type, role.BypassRLS),
			limit, password, expiry,
		})
	}
	if err := in.Context.Err(); err != nil {
		return nil, err
	}
	return rows, nil
}
