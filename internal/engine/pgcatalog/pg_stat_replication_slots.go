package pgcatalog

import (
	"fmt"

	"stuntgres/internal/engine/ids"
	"stuntgres/internal/sqltype"
)

// Relation identity comes from docs/design/regtype-pg18-inventory.tsv.
const StatReplicationSlotsOID ids.OID = 12266

var replicationSlotsColumns = []ColumnSpec{
	{"slot_name", sqltype.Text, true, 100, 0, -1},
	{"spill_txns", sqltype.Int8, true, 0, 0, -1},
	{"spill_count", sqltype.Int8, true, 0, 0, -1},
	{"spill_bytes", sqltype.Int8, true, 0, 0, -1},
	{"stream_txns", sqltype.Int8, true, 0, 0, -1},
	{"stream_count", sqltype.Int8, true, 0, 0, -1},
	{"stream_bytes", sqltype.Int8, true, 0, 0, -1},
	{"total_txns", sqltype.Int8, true, 0, 0, -1},
	{"total_bytes", sqltype.Int8, true, 0, 0, -1},
	{"stats_reset", sqltype.Timestamptz, true, 0, 0, -1},
}

func capturePGStatReplicationSlots(in Input) ([][]sqltype.Value, error) {
	if err := in.Context.Err(); err != nil {
		return nil, err
	}
	if in.Owner == nil || in.Catalog.Owner() != in.Owner || !RegisteredSource(in.Catalog, in.Source) ||
		in.Source.Address().ObjectID != StatReplicationSlotsOID || in.Ref.Domain() != in.Owner.Domain ||
		in.Ref.Epoch != in.Catalog.Epoch() || in.Ref.Address != in.Source.Address() ||
		!in.Roles.BelongsTo(in.Owner.Principals) {
		return nil, fmt.Errorf("stuntgres: invalid replication slots source authority")
	}
	for i, column := range in.Source.Columns {
		if column.Usage.Typmod != -1 || column.Usage.Collation.ID != replicationSlotsColumns[i].Collation || column.DeclaredDimensions != 0 || !column.Nullable || !column.NullabilityKnown {
			return nil, fmt.Errorf("stuntgres: invalid replication slots source attribute")
		}
	}
	// Every logical slot has a statistics entry from creation. Decoding
	// counters are not collected; they stay zero until reset.
	if len(in.ReplicationSlots) == 0 {
		return nil, in.Context.Err()
	}
	rows := make([][]sqltype.Value, 0, len(in.ReplicationSlots))
	for _, slot := range in.ReplicationSlots {
		row := make([]sqltype.Value, len(replicationSlotsColumns))
		row[0] = sqltype.String(in.Source.Columns[0].Usage.Type, slot.Name)
		for i := 1; i < 9; i++ {
			v, err := sqltype.Integer(in.Source.Columns[i].Usage.Type, 0)
			if err != nil {
				return nil, err
			}
			row[i] = v
		}
		row[9] = sqltype.Null(in.Source.Columns[9].Usage.Type)
		if !slot.StatsReset.IsZero() {
			v, err := sqltype.TimestampValue(in.Source.Columns[9].Usage.Type, slot.StatsReset.UnixMicro()-postgresEpochUnixMicros)
			if err != nil {
				return nil, err
			}
			row[9] = v
		}
		rows = append(rows, row)
	}
	return rows, in.Context.Err()
}

// Display only: released PG18 logical-slot inventory and lateral statistics
// join. These identities do not confer callable backing implementations.
var replicationSlotsViewParts = func() []builtinViewPart {
	parts := []builtinViewPart{viewText(" SELECT ")}
	for i, column := range replicationSlotsColumns {
		if i != 0 {
			parts = append(parts, viewText(",\n    "))
		}
		parts = append(parts, viewIdentifier("s"), viewText("."), viewIdentifier(column.Name))
	}
	parts = append(parts,
		viewText("\n   FROM "), viewClass("pg_replication_slots"), viewText(" "), viewIdentifier("r"),
		viewText(",\n    LATERAL "), viewRoutine("pg_stat_get_replication_slot", "25"),
		viewText("(("), viewIdentifier("r"), viewText("."), viewIdentifier("slot_name"), viewText(")::"), viewType("text"), viewText(") "), viewIdentifier("s"), viewText("("))
	for i, column := range replicationSlotsColumns {
		if i != 0 {
			parts = append(parts, viewText(", "))
		}
		parts = append(parts, viewIdentifier(column.Name))
	}
	return append(parts, viewText(")\n  WHERE ("), viewIdentifier("r"), viewText("."), viewIdentifier("datoid"), viewText(" IS NOT NULL);"))
}()
