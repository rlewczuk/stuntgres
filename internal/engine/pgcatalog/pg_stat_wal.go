package pgcatalog

import (
	"fmt"

	"stuntgres/internal/engine/ids"
	"stuntgres/internal/sqltype"
)

// Relation identity comes from docs/design/regtype-pg18-inventory.tsv.
const StatWALOID ids.OID = 12305

// Native identity for released _RETURN metadata, not a portable initdb OID.
const StatWALRuleOID ids.OID = 12308

var walColumns = []ColumnSpec{
	{"wal_records", sqltype.Int8, true, 0, 0, -1},
	{"wal_fpi", sqltype.Int8, true, 0, 0, -1},
	{"wal_bytes", sqltype.Numeric, true, 0, 0, -1},
	{"wal_buffers_full", sqltype.Int8, true, 0, 0, -1},
	{"stats_reset", sqltype.Timestamptz, true, 0, 0, -1},
}

func capturePGStatWAL(in Input) ([][]sqltype.Value, error) {
	if err := in.Context.Err(); err != nil {
		return nil, err
	}
	if in.Owner == nil || in.Catalog.Owner() != in.Owner || !RegisteredSource(in.Catalog, in.Source) ||
		in.Source.Address().ObjectID != StatWALOID || in.Ref.Domain() != in.Owner.Domain ||
		in.Ref.Epoch != in.Catalog.Epoch() || in.Ref.Address != in.Source.Address() ||
		!in.Roles.BelongsTo(in.Owner.Principals) {
		return nil, fmt.Errorf("stuntgres: invalid WAL source authority")
	}
	for _, column := range in.Source.Columns {
		if column.Usage.Typmod != -1 || column.Usage.Collation.ID != 0 || column.DeclaredDimensions != 0 || !column.Nullable || !column.NullabilityKnown {
			return nil, fmt.Errorf("stuntgres: invalid WAL source attribute")
		}
	}
	row := make([]sqltype.Value, len(walColumns))
	// No physical WAL generator, full-page images or buffers exist. These
	// technical estimates are exactly zero, never inferred from keyed IO facts
	// or unrelated SharedFact slots. Only ResetAt has shared event authority.
	for _, column := range [...]int{0, 1, 3} {
		value, err := sqltype.Integer(in.Source.Columns[column].Usage.Type, 0)
		if err != nil {
			return nil, err
		}
		row[column] = value
	}
	bytes, err := sqltype.NumericUint64Value(in.Source.Columns[2].Usage.Type, 0)
	if err != nil {
		return nil, err
	}
	row[2] = bytes
	row[4] = sqltype.Null(in.Source.Columns[4].Usage.Type)
	if !in.WAL.ResetAt.IsZero() {
		stamp, err := sqltype.TimestampFromTime(in.Source.Columns[4].Usage.Type, in.WAL.ResetAt.UTC())
		if err != nil {
			return nil, err
		}
		row[4] = stamp
	}
	if err := in.Context.Err(); err != nil {
		return nil, err
	}
	return [][]sqltype.Value{row}, nil
}

// Display only: shared-provider capture owns execution, not a callable getter.
var walViewParts = func() []builtinViewPart {
	parts := []builtinViewPart{viewText(" SELECT ")}
	for i, column := range walColumns {
		if i != 0 {
			parts = append(parts, viewText(",\n    "))
		}
		parts = append(parts, viewIdentifier(column.Name))
	}
	parts = append(parts, viewText("\n   FROM "), viewRoutine("pg_stat_get_wal", ""), viewText("() "), viewIdentifier("w"), viewText("("))
	for i, column := range walColumns {
		if i != 0 {
			parts = append(parts, viewText(", "))
		}
		parts = append(parts, viewIdentifier(column.Name))
	}
	return append(parts, viewText(");"))
}()
