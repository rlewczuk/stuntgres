package pgcatalog

import (
	"fmt"

	"stuntgres/internal/engine/ids"
	"stuntgres/internal/sqltype"
)

// Relation identity comes from docs/design/regtype-pg18-inventory.tsv.
const StatRecoveryPrefetchOID ids.OID = 12244

var recoveryPrefetchColumns = []ColumnSpec{
	{"stats_reset", sqltype.Timestamptz, true, 0, 0, -1},
	{"prefetch", sqltype.Int8, true, 0, 0, -1},
	{"hit", sqltype.Int8, true, 0, 0, -1},
	{"skip_init", sqltype.Int8, true, 0, 0, -1},
	{"skip_new", sqltype.Int8, true, 0, 0, -1},
	{"skip_fpw", sqltype.Int8, true, 0, 0, -1},
	{"skip_rep", sqltype.Int8, true, 0, 0, -1},
	{"wal_distance", sqltype.Int4, true, 0, 0, -1},
	{"block_distance", sqltype.Int4, true, 0, 0, -1},
	{"io_depth", sqltype.Int4, true, 0, 0, -1},
}

func capturePGStatRecoveryPrefetch(in Input) ([][]sqltype.Value, error) {
	if err := in.Context.Err(); err != nil {
		return nil, err
	}
	if in.Owner == nil || in.Catalog.Owner() != in.Owner || !RegisteredSource(in.Catalog, in.Source) ||
		in.Source.Address().ObjectID != StatRecoveryPrefetchOID || in.Ref.Domain() != in.Owner.Domain ||
		in.Ref.Epoch != in.Catalog.Epoch() || in.Ref.Address != in.Source.Address() ||
		!in.Roles.BelongsTo(in.Owner.Principals) {
		return nil, fmt.Errorf("stuntgres: invalid recovery prefetch source authority")
	}
	for _, column := range in.Source.Columns {
		if column.Usage.Typmod != -1 || column.Usage.Collation.ID != 0 || column.DeclaredDimensions != 0 || !column.Nullable || !column.NullabilityKnown {
			return nil, fmt.Errorf("stuntgres: invalid recovery prefetch source attribute")
		}
	}
	row := make([]sqltype.Value, 10)
	row[0] = sqltype.Null(in.Source.Columns[0].Usage.Type)
	if !in.RecoveryPrefetch.ResetAt.IsZero() {
		stamp, err := sqltype.TimestampFromTime(in.Source.Columns[0].Usage.Type, in.RecoveryPrefetch.ResetAt.UTC())
		if err != nil {
			return nil, err
		}
		row[0] = stamp
	}
	fact := &in.RecoveryPrefetch
	values := [...]int64{fact.Counters[0], fact.Counters[1], fact.Counters[2], fact.Counters[3], fact.Counters[4], fact.Counters[5], fact.WALDistance, fact.BlockDistance, fact.IODepth}
	for i, n := range values {
		value, err := sqltype.Integer(in.Source.Columns[i+1].Usage.Type, n)
		if err != nil {
			return nil, err
		}
		row[i+1] = value
	}
	if err := in.Context.Err(); err != nil {
		return nil, err
	}
	return [][]sqltype.Value{row}, nil
}

// Display only: the shared-provider producer owns execution. The backing routine
// identity does not grant a callable SQL implementation.
var recoveryPrefetchViewParts = func() []builtinViewPart {
	parts := []builtinViewPart{viewText(" SELECT ")}
	for i, column := range recoveryPrefetchColumns {
		if i != 0 {
			parts = append(parts, viewText(",\n    "))
		}
		parts = append(parts, viewIdentifier(column.Name))
	}
	parts = append(parts, viewText("\n   FROM "), viewRoutine("pg_stat_get_recovery_prefetch", ""), viewText("() "), viewIdentifier("s"), viewText("("))
	for i, column := range recoveryPrefetchColumns {
		if i != 0 {
			parts = append(parts, viewText(", "))
		}
		parts = append(parts, viewIdentifier(column.Name))
	}
	return append(parts, viewText(");"))
}()
