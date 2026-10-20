package pgcatalog

import (
	"math"
	"stuntgres/internal/sqltype"
)

// Shared comments follow principal identities, not database-local descriptors.
// The caller supplies the statement's saved RoleRead, including nested views.
func capturePGShDescription(in Input) ([][]sqltype.Value, error) {
	ctx := in.Context
	roles := in.Roles
	source := in.Source
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	class, err := sqltype.OIDValue(source.Columns[1].Usage.Type, 1260)
	if err != nil {
		return nil, err
	}
	rows := make([][]sqltype.Value, 0)
	for principal, role := range roles.RoleEntries() {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		if role.Comment == "" || principal.ID() > math.MaxUint32 {
			continue
		}
		object, err := sqltype.OIDValue(source.Columns[0].Usage.Type, uint32(principal.ID()))
		if err != nil {
			return nil, err
		}
		rows = append(rows, []sqltype.Value{object, class, sqltype.String(source.Columns[2].Usage.Type, role.Comment)})
	}
	return rows, nil
}
