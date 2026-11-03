package pgcatalog

import (
	"fmt"

	"stuntgres/internal/engine/ids"
	"stuntgres/internal/engine/statistics"
	"stuntgres/internal/sqltype"
)

const StatSLRUOID ids.OID = 12236

var slruColumns = []ColumnSpec{
	{"name", sqltype.Text, true, 100, 0, -1},
	{"blks_zeroed", sqltype.Int8, true, 0, 0, -1},
	{"blks_hit", sqltype.Int8, true, 0, 0, -1},
	{"blks_read", sqltype.Int8, true, 0, 0, -1},
	{"blks_written", sqltype.Int8, true, 0, 0, -1},
	{"blks_exists", sqltype.Int8, true, 0, 0, -1},
	{"flushes", sqltype.Int8, true, 0, 0, -1},
	{"truncates", sqltype.Int8, true, 0, 0, -1},
	{"stats_reset", sqltype.Timestamptz, true, 0, 0, -1},
}

func capturePGStatSLRU(in Input) ([][]sqltype.Value, error) {
	if err := in.Context.Err(); err != nil {
		return nil, err
	}
	if in.Owner == nil || in.Catalog.Owner() != in.Owner || !RegisteredSource(in.Catalog, in.Source) ||
		in.Source.Address().ObjectID != StatSLRUOID || in.Ref.Domain() != in.Owner.Domain ||
		in.Ref.Epoch != in.Catalog.Epoch() || in.Ref.Address != in.Source.Address() ||
		!in.Roles.BelongsTo(in.Owner.Principals) {
		return nil, fmt.Errorf("stuntgres: invalid SLRU source authority")
	}
	for i, column := range in.Source.Columns {
		collation := uint32(0)
		if i == 0 {
			collation = 100
		}
		if column.Usage.Typmod != -1 || column.Usage.Collation.ID != collation || column.DeclaredDimensions != 0 || !column.Nullable || !column.NullabilityKnown {
			return nil, fmt.Errorf("stuntgres: invalid SLRU source attribute")
		}
	}
	rows := make([][]sqltype.Value, statistics.SLRUCount)
	for k := range in.SLRU {
		fact := &in.SLRU[k]
		row := make([]sqltype.Value, len(slruColumns))
		row[0] = sqltype.String(in.Source.Columns[0].Usage.Type, statistics.SLRUKind(k).Name())
		for i, n := range fact.Counters {
			value, err := sqltype.Integer(in.Source.Columns[i+1].Usage.Type, n)
			if err != nil {
				return nil, err
			}
			row[i+1] = value
		}
		row[8] = sqltype.Null(in.Source.Columns[8].Usage.Type)
		if !fact.ResetAt.IsZero() {
			stamp, err := sqltype.TimestampFromTime(in.Source.Columns[8].Usage.Type, fact.ResetAt.UTC())
			if err != nil {
				return nil, err
			}
			row[8] = stamp
		}
		rows[k] = row
	}
	if err := in.Context.Err(); err != nil {
		return nil, err
	}
	return rows, nil
}

// Display/dependency identity only, not a callable getter implementation.
var slruViewParts = func() []builtinViewPart {
	parts := []builtinViewPart{viewText(" SELECT ")}
	for i, column := range slruColumns {
		if i != 0 {
			parts = append(parts, viewText(",\n    "))
		}
		parts = append(parts, viewIdentifier(column.Name))
	}
	parts = append(parts, viewText("\n   FROM "), viewRoutine("pg_stat_get_slru", ""), viewText("() "), viewIdentifier("s"), viewText("("))
	for i, column := range slruColumns {
		if i != 0 {
			parts = append(parts, viewText(", "))
		}
		parts = append(parts, viewIdentifier(column.Name))
	}
	return append(parts, viewText(");"))
}()
