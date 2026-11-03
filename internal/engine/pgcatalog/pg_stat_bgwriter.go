package pgcatalog

import (
	"fmt"

	"stuntgres/internal/engine/ids"
	"stuntgres/internal/sqltype"
)

// Relation identity comes from docs/design/regtype-pg18-inventory.tsv.
const StatBGWriterOID ids.OID = 12293

var bgwriterColumns = []ColumnSpec{
	{"buffers_clean", sqltype.Int8, true, 0, 0, -1},
	{"maxwritten_clean", sqltype.Int8, true, 0, 0, -1},
	{"buffers_alloc", sqltype.Int8, true, 0, 0, -1},
	{"stats_reset", sqltype.Timestamptz, true, 0, 0, -1},
}

func capturePGStatBGWriter(in Input) ([][]sqltype.Value, error) {
	if err := in.Context.Err(); err != nil {
		return nil, err
	}
	if in.Owner == nil || in.Catalog.Owner() != in.Owner || !RegisteredSource(in.Catalog, in.Source) ||
		in.Source.Address().ObjectID != StatBGWriterOID || in.Ref.Domain() != in.Owner.Domain ||
		in.Ref.Epoch != in.Catalog.Epoch() || in.Ref.Address != in.Source.Address() ||
		!in.Roles.BelongsTo(in.Owner.Principals) {
		return nil, fmt.Errorf("stuntgres: invalid bgwriter source authority")
	}
	for _, column := range in.Source.Columns {
		if column.Usage.Typmod != -1 || column.Usage.Collation.ID != 0 || column.DeclaredDimensions != 0 || !column.Nullable || !column.NullabilityKnown {
			return nil, fmt.Errorf("stuntgres: invalid bgwriter source attribute")
		}
	}
	row := make([]sqltype.Value, len(bgwriterColumns))
	// Slots 0/1/2 are buffers cleaned, cleaning-limit stops and allocations.
	// With no physical publisher their deterministic technical estimate is zero;
	// ordinary SQL and logical buffercache activity never synthesize these events.
	for i := range 3 {
		value, err := sqltype.Integer(in.Source.Columns[i].Usage.Type, in.BGWriter.Counters[i])
		if err != nil {
			return nil, err
		}
		row[i] = value
	}
	row[3] = sqltype.Null(in.Source.Columns[3].Usage.Type)
	if !in.BGWriter.ResetAt.IsZero() {
		value, err := sqltype.TimestampFromTime(in.Source.Columns[3].Usage.Type, in.BGWriter.ResetAt.UTC())
		if err != nil {
			return nil, err
		}
		row[3] = value
	}
	if err := in.Context.Err(); err != nil {
		return nil, err
	}
	return [][]sqltype.Value{row}, nil
}

// Display only: execution projects the admitted Reader fact, not callable getters.
var bgwriterViewParts = func() []builtinViewPart {
	parts := []builtinViewPart{viewText(" SELECT ")}
	for i, name := range [...]string{"pg_stat_get_bgwriter_buf_written_clean", "pg_stat_get_bgwriter_maxwritten_clean", "pg_stat_get_buf_alloc", "pg_stat_get_bgwriter_stat_reset_time"} {
		if i != 0 {
			parts = append(parts, viewText(",\n    "))
		}
		parts = append(parts, viewRoutine(name, ""), viewText("() AS "), viewIdentifier(bgwriterColumns[i].Name))
	}
	return append(parts, viewText(";"))
}()
