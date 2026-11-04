package pgcatalog

import (
	"fmt"

	"stuntgres/internal/engine/ids"
	"stuntgres/internal/sqltype"
)

const ReplicationSlotsOID ids.OID = 12261

var slotInventoryColumns = []ColumnSpec{
	{"slot_name", sqltype.Name, true, 950, 0, -1},
	{"plugin", sqltype.Name, true, 950, 0, -1},
	{"slot_type", sqltype.Text, true, 100, 0, -1},
	{"datoid", sqltype.Oid, true, 0, 0, -1},
	{"database", sqltype.Name, true, 950, 0, -1},
	{"temporary", sqltype.Bool, true, 0, 0, -1},
	{"active", sqltype.Bool, true, 0, 0, -1},
	{"active_pid", sqltype.Int4, true, 0, 0, -1},
	{"xmin", sqltype.Xid, true, 0, 0, -1},
	{"catalog_xmin", sqltype.Xid, true, 0, 0, -1},
	{"restart_lsn", sqltype.PgLSN, true, 0, 0, -1},
	{"confirmed_flush_lsn", sqltype.PgLSN, true, 0, 0, -1},
	{"wal_status", sqltype.Text, true, 100, 0, -1},
	{"safe_wal_size", sqltype.Int8, true, 0, 0, -1},
	{"two_phase", sqltype.Bool, true, 0, 0, -1},
	{"two_phase_at", sqltype.PgLSN, true, 0, 0, -1},
	{"inactive_since", sqltype.Timestamptz, true, 0, 0, -1},
	{"conflicting", sqltype.Bool, true, 0, 0, -1},
	{"invalidation_reason", sqltype.Text, true, 100, 0, -1},
	{"failover", sqltype.Bool, true, 0, 0, -1},
	{"synced", sqltype.Bool, true, 0, 0, -1},
}

func capturePGReplicationSlots(in Input) ([][]sqltype.Value, error) {
	if in.Context == nil {
		return nil, fmt.Errorf("stuntgres: invalid replication slot inventory context")
	}
	if err := in.Context.Err(); err != nil {
		return nil, err
	}
	address := ids.ObjectAddress{ClassID: ids.RelationClassID, ObjectID: ReplicationSlotsOID}
	if in.Owner == nil || in.Catalog.Owner() != in.Owner || !RegisteredSource(in.Catalog, in.Source) || in.Source.Address() != address || in.Ref.Domain() != in.Owner.Domain || in.Ref.Epoch != in.Catalog.Epoch() || in.Ref.Address != address || !in.Roles.BelongsTo(in.Owner.Principals) {
		return nil, fmt.Errorf("stuntgres: invalid replication slot inventory source")
	}
	for i, column := range in.Source.Columns {
		if column.Usage.Typmod != -1 || column.Usage.Collation.ID != slotInventoryColumns[i].Collation || column.DeclaredDimensions != 0 || !column.Nullable || !column.NullabilityKnown {
			return nil, fmt.Errorf("stuntgres: invalid replication slot inventory attribute")
		}
	}
	// Creation rejects before publishing any slot or WAL state. Neither a
	// statistics key nor an origin/subscription can establish slot inventory.
	return nil, in.Context.Err()
}

func slotInventoryViewParts(pretty bool) []builtinViewPart {
	parts := []builtinViewPart{viewText(" SELECT ")}
	for i, column := range slotInventoryColumns {
		if i != 0 {
			parts = append(parts, viewText(",\n    "))
		}
		if column.Name == "database" {
			parts = append(parts, viewIdentifier("d"), viewText("."), viewIdentifier("datname"), viewText(" AS "), viewIdentifier("database"))
		} else {
			parts = append(parts, viewIdentifier("l"), viewText("."), viewIdentifier(column.Name))
		}
	}
	from := "\n   FROM ("
	if pretty {
		from = "\n   FROM "
	}
	parts = append(parts, viewText(from), viewRoutine("pg_get_replication_slots", ""), viewText("() "), viewIdentifier("l"), viewText("("))
	first := true
	for _, column := range slotInventoryColumns {
		if column.Name == "database" {
			continue
		}
		if !first {
			parts = append(parts, viewText(", "))
		}
		first = false
		parts = append(parts, viewIdentifier(column.Name))
	}
	parts = append(parts, viewText(")\n     LEFT JOIN "), viewClass("pg_database"), viewText(" "), viewIdentifier("d"))
	if pretty {
		return append(parts, viewText(" ON "), viewIdentifier("l"), viewText("."), viewIdentifier("datoid"), viewText(" = "), viewIdentifier("d"), viewText("."), viewIdentifier("oid"), viewText(";"))
	}
	return append(parts, viewText(" ON (("), viewIdentifier("l"), viewText("."), viewIdentifier("datoid"), viewText(" = "), viewIdentifier("d"), viewText("."), viewIdentifier("oid"), viewText(")));"))
}
