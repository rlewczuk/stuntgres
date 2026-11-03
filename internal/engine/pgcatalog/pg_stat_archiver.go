package pgcatalog

import (
	"fmt"
	"time"

	"stuntgres/internal/engine/ids"
	"stuntgres/internal/sqltype"
)

// Relation identity comes from docs/design/regtype-pg18-inventory.tsv.
const StatArchiverOID ids.OID = 12289

var archiverColumns = []ColumnSpec{
	{"archived_count", sqltype.Int8, true, 0, 0, -1},
	{"last_archived_wal", sqltype.Text, true, 0, 0, -1},
	{"last_archived_time", sqltype.Timestamptz, true, 0, 0, -1},
	{"failed_count", sqltype.Int8, true, 0, 0, -1},
	{"last_failed_wal", sqltype.Text, true, 0, 0, -1},
	{"last_failed_time", sqltype.Timestamptz, true, 0, 0, -1},
	{"stats_reset", sqltype.Timestamptz, true, 0, 0, -1},
}

func capturePGStatArchiver(in Input) ([][]sqltype.Value, error) {
	if err := in.Context.Err(); err != nil {
		return nil, err
	}
	if in.Owner == nil || in.Catalog.Owner() != in.Owner || !RegisteredSource(in.Catalog, in.Source) ||
		in.Source.Address().ObjectID != StatArchiverOID || in.Ref.Domain() != in.Owner.Domain ||
		in.Ref.Epoch != in.Catalog.Epoch() || in.Ref.Address != in.Source.Address() ||
		!in.Roles.BelongsTo(in.Owner.Principals) {
		return nil, fmt.Errorf("stuntgres: invalid archiver source authority")
	}
	for i, column := range in.Source.Columns {
		collation := uint32(0)
		if i == 1 || i == 4 {
			collation = 100
		}
		if column.Usage.Typmod != -1 || column.Usage.Collation.ID != collation || column.DeclaredDimensions != 0 || !column.Nullable || !column.NullabilityKnown {
			return nil, fmt.Errorf("stuntgres: invalid archiver source attribute")
		}
	}
	row := make([]sqltype.Value, len(archiverColumns))
	for i, column := range in.Source.Columns {
		row[i] = sqltype.Null(column.Usage.Type)
	}
	// Archiver owns slots 0 (successful WAL files) and 1 (failed attempts).
	// There is no physical publisher; SQL activity never creates these events.
	for slot, column := range [...]int{0, 3} {
		value, err := sqltype.Integer(in.Source.Columns[column].Usage.Type, in.Archiver.Counters[slot])
		if err != nil {
			return nil, err
		}
		row[column] = value
	}
	for i, name := range [...]string{in.Archiver.LastSuccess, in.Archiver.LastFailure} {
		column := 1 + i*3
		if name != "" {
			row[column] = sqltype.String(in.Source.Columns[column].Usage.Type, name)
		}
	}
	for i, stamp := range [...]time.Time{in.Archiver.LastSuccessAt, in.Archiver.LastFailureAt, in.Archiver.ResetAt} {
		column := [...]int{2, 5, 6}[i]
		if !stamp.IsZero() {
			value, err := sqltype.TimestampFromTime(in.Source.Columns[column].Usage.Type, stamp.UTC())
			if err != nil {
				return nil, err
			}
			row[column] = value
		}
	}
	if err := in.Context.Err(); err != nil {
		return nil, err
	}
	return [][]sqltype.Value{row}, nil
}

// Display only: the shared-provider producer owns execution, not a callable getter.
var archiverViewParts = func() []builtinViewPart {
	parts := []builtinViewPart{viewText(" SELECT ")}
	for i, column := range archiverColumns {
		if i != 0 {
			parts = append(parts, viewText(",\n    "))
		}
		parts = append(parts, viewIdentifier(column.Name))
	}
	parts = append(parts, viewText("\n   FROM "), viewRoutine("pg_stat_get_archiver", ""), viewText("() "), viewIdentifier("s"), viewText("("))
	for i, column := range archiverColumns {
		if i != 0 {
			parts = append(parts, viewText(", "))
		}
		parts = append(parts, viewIdentifier(column.Name))
	}
	return append(parts, viewText(");"))
}()
