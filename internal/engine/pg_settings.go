package engine

import (
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/sqltype"
)

// Reserved native relation identity, not a PostgreSQL allocator promise.
const pgSettingsOID ids.OID = 16202

// The supported inventory is exactly row_security. Capture one coherent physical
// session observation; returned rows retain neither settings locks nor authority.
func capturePGSettings(in catalogSourceInput) ([][]sqltype.Value, error) {
	ctx, d := in.Context, in.Source
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	fact := in.Runtime.SessionSettings()
	value, reset, source := fact.Value, fact.Reset, fact.Source
	row := make([]sqltype.Value, len(d.Columns))
	for i, column := range d.Columns {
		row[i] = sqltype.Null(column.Usage.Type)
	}
	for _, field := range []struct {
		index int
		value string
	}{
		{0, "row_security"}, {1, value}, {3, "Client Connection Defaults / Statement Behavior"},
		{4, "Enables row security."}, {5, "When enabled, row security will be applied to all users."},
		{6, "user"}, {7, "bool"}, {8, source}, {12, "on"}, {13, reset},
	} {
		row[field.index] = sqltype.String(d.Columns[field.index].Usage.Type, field.value)
	}
	row[16] = sqltype.Boolean(d.Columns[16].Usage.Type, false)
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	return [][]sqltype.Value{row}, nil
}
