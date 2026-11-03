package pgcatalog

import (
	"fmt"
	"time"

	"stuntgres/internal/engine/ids"
	"stuntgres/internal/sqltype"
)

// Relation identity comes from docs/design/regtype-pg18-inventory.tsv.
const StatCheckpointerOID ids.OID = 12297

var checkpointerColumns = []ColumnSpec{
	{"num_timed", sqltype.Int8, true, 0, 0, -1},
	{"num_requested", sqltype.Int8, true, 0, 0, -1},
	{"num_done", sqltype.Int8, true, 0, 0, -1},
	{"restartpoints_timed", sqltype.Int8, true, 0, 0, -1},
	{"restartpoints_req", sqltype.Int8, true, 0, 0, -1},
	{"restartpoints_done", sqltype.Int8, true, 0, 0, -1},
	{"write_time", sqltype.Float8, true, 0, 0, -1},
	{"sync_time", sqltype.Float8, true, 0, 0, -1},
	{"buffers_written", sqltype.Int8, true, 0, 0, -1},
	{"slru_written", sqltype.Int8, true, 0, 0, -1},
	{"stats_reset", sqltype.Timestamptz, true, 0, 0, -1},
}

func capturePGStatCheckpointer(in Input) ([][]sqltype.Value, error) {
	if err := in.Context.Err(); err != nil {
		return nil, err
	}
	if in.Owner == nil || in.Catalog.Owner() != in.Owner || !RegisteredSource(in.Catalog, in.Source) ||
		in.Source.Address().ObjectID != StatCheckpointerOID || in.Ref.Domain() != in.Owner.Domain ||
		in.Ref.Epoch != in.Catalog.Epoch() || in.Ref.Address != in.Source.Address() ||
		!in.Roles.BelongsTo(in.Owner.Principals) {
		return nil, fmt.Errorf("stuntgres: invalid checkpointer source authority")
	}
	for _, column := range in.Source.Columns {
		if column.Usage.Typmod != -1 || column.Usage.Collation.ID != 0 || column.DeclaredDimensions != 0 || !column.Nullable || !column.NullabilityKnown {
			return nil, fmt.Errorf("stuntgres: invalid checkpointer source attribute")
		}
	}
	row := make([]sqltype.Value, len(checkpointerColumns))
	// All eight counters are independent. Logical SQL never publishes physical
	// checkpoint events; an absent facility therefore has deterministic zeros.
	for slot, n := range in.Checkpointer.Counters {
		column := slot
		if slot >= 6 {
			column += 2
		}
		value, err := sqltype.Integer(in.Source.Columns[column].Usage.Type, n)
		if err != nil {
			return nil, err
		}
		row[column] = value
	}
	for i, duration := range [...]time.Duration{in.Checkpointer.WriteTime, in.Checkpointer.SyncTime} {
		value, err := sqltype.Float(in.Source.Columns[6+i].Usage.Type, float64(duration)/float64(time.Millisecond))
		if err != nil {
			return nil, err
		}
		row[6+i] = value
	}
	row[10] = sqltype.Null(in.Source.Columns[10].Usage.Type)
	if !in.Checkpointer.ResetAt.IsZero() {
		value, err := sqltype.TimestampFromTime(in.Source.Columns[10].Usage.Type, in.Checkpointer.ResetAt.UTC())
		if err != nil {
			return nil, err
		}
		row[10] = value
	}
	if err := in.Context.Err(); err != nil {
		return nil, err
	}
	return [][]sqltype.Value{row}, nil
}

// Display only: execution projects the admitted Reader fact, not callable getters.
var checkpointerViewParts = func() []builtinViewPart {
	parts := []builtinViewPart{viewText(" SELECT ")}
	for i, name := range [...]string{"num_timed", "num_requested", "num_performed", "restartpoints_timed", "restartpoints_requested", "restartpoints_performed", "write_time", "sync_time", "buffers_written", "slru_written", "stat_reset_time"} {
		if i != 0 {
			parts = append(parts, viewText(",\n    "))
		}
		parts = append(parts, viewRoutine("pg_stat_get_checkpointer_"+name, ""), viewText("() AS "), viewIdentifier(checkpointerColumns[i].Name))
	}
	return append(parts, viewText(";"))
}()
