package pgcatalog

import (
	"math"

	"stuntgres/internal/engine/ids"
	"stuntgres/internal/sqltype"
)

// ShadowOID identifies the sensitive role view, independently of its row type.
const ShadowOID ids.OID = 12013

// Authorization belongs to the common relation manifest and stored-view owner
// checks. This trusted projection only reads the statement's immutable role root.
func capturePGShadow(in Input) ([][]sqltype.Value, error) {
	if err := in.Context.Err(); err != nil {
		return nil, err
	}
	var rows [][]sqltype.Value
	columns := in.Source.Columns
	for principal, role := range in.Roles.RoleEntries() {
		if err := in.Context.Err(); err != nil {
			return nil, err
		}
		if !role.Login || principal.ID() > math.MaxUint32 {
			continue
		}
		id, err := sqltype.OIDValue(columns[1].Usage.Type, uint32(principal.ID()))
		if err != nil {
			return nil, err
		}
		password := sqltype.Null(columns[6].Usage.Type)
		if role.Password != "" {
			password = sqltype.String(columns[6].Usage.Type, role.Password)
		}
		expiry := sqltype.Null(columns[7].Usage.Type)
		if role.HasValidUntil {
			expiry, err = sqltype.TimestampValue(columns[7].Usage.Type, role.ValidUntil)
			if err != nil {
				return nil, err
			}
		}
		config, err := roleConfig(in, principal, columns[8].Usage.Type)
		if err != nil {
			return nil, err
		}
		rows = append(rows, []sqltype.Value{
			sqltype.String(columns[0].Usage.Type, role.Name), id,
			sqltype.Boolean(columns[2].Usage.Type, role.CreateDB),
			sqltype.Boolean(columns[3].Usage.Type, role.Superuser),
			sqltype.Boolean(columns[4].Usage.Type, role.Replication),
			sqltype.Boolean(columns[5].Usage.Type, role.BypassRLS),
			password, expiry, config,
		})
	}
	if err := in.Context.Err(); err != nil {
		return nil, err
	}
	return rows, nil
}
