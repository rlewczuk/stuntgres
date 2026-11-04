package pgcatalog

import (
	"fmt"
	"time"

	"stuntgres/internal/engine/ids"
	"stuntgres/internal/sqltype"
)

// ReplicationSlot is an immutable snapshot of one logical replication slot.
// Zero positions, PID and times render as NULL.
type ReplicationSlot struct {
	Name, Plugin, Database string
	DatabaseOID            ids.OID
	Temporary              bool
	ActivePID              int32
	CatalogXmin            uint32
	Restart, Confirmed     uint64
	TwoPhase               bool
	TwoPhaseAt             uint64
	InactiveSince          time.Time
	Failover               bool
	StatsReset             time.Time
}

const postgresEpochUnixMicros int64 = 946684800000000

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
	if len(in.ReplicationSlots) == 0 {
		return nil, in.Context.Err()
	}
	rows := make([][]sqltype.Value, 0, len(in.ReplicationSlots))
	for _, slot := range in.ReplicationSlots {
		row, err := slotInventoryRow(in, slot)
		if err != nil {
			return nil, err
		}
		rows = append(rows, row)
	}
	return rows, in.Context.Err()
}

func slotInventoryRow(in Input, slot ReplicationSlot) ([]sqltype.Value, error) {
	row := make([]sqltype.Value, len(slotInventoryColumns))
	for i := range row {
		row[i] = sqltype.Null(in.Source.Columns[i].Usage.Type)
	}
	typ := func(i int) *sqltype.Descriptor { return in.Source.Columns[i].Usage.Type }
	var err error
	set := func(i int, v sqltype.Value, e error) {
		if err == nil {
			row[i], err = v, e
		}
	}
	lsn := func(i int, n uint64) {
		if n != 0 {
			v, e := sqltype.PGLSNValue(typ(i), n)
			set(i, v, e)
		}
	}
	row[0], row[1], row[2] = sqltype.String(typ(0), slot.Name), sqltype.String(typ(1), slot.Plugin), sqltype.String(typ(2), "logical")
	datoid, e := sqltype.OIDValue(typ(3), uint32(slot.DatabaseOID))
	set(3, datoid, e)
	row[4], row[5], row[6] = sqltype.String(typ(4), slot.Database), sqltype.Boolean(typ(5), slot.Temporary), sqltype.Boolean(typ(6), slot.ActivePID != 0)
	if slot.ActivePID != 0 {
		v, e := sqltype.Integer(typ(7), int64(slot.ActivePID))
		set(7, v, e)
	}
	if slot.CatalogXmin != 0 {
		v, e := sqltype.XIDValue(typ(9), slot.CatalogXmin)
		set(9, v, e)
	}
	lsn(10, slot.Restart)
	lsn(11, slot.Confirmed)
	if slot.Restart != 0 {
		row[12] = sqltype.String(typ(12), "reserved")
	}
	row[14] = sqltype.Boolean(typ(14), slot.TwoPhase)
	lsn(15, slot.TwoPhaseAt)
	if !slot.InactiveSince.IsZero() {
		v, e := sqltype.TimestampValue(typ(16), slot.InactiveSince.UnixMicro()-postgresEpochUnixMicros)
		set(16, v, e)
	}
	row[17], row[19], row[20] = sqltype.Boolean(typ(17), false), sqltype.Boolean(typ(19), slot.Failover), sqltype.Boolean(typ(20), false)
	return row, err
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
