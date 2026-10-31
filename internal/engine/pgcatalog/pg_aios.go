package pgcatalog

import (
	"fmt"

	"stuntgres/internal/engine/ids"
	"stuntgres/internal/sqltype"
)

const AIOSOID ids.OID = 12355

var aiosColumns = []ColumnSpec{
	{"pid", sqltype.Int4, true, 0, 0, -1},
	{"io_id", sqltype.Int4, true, 0, 0, -1},
	{"io_generation", sqltype.Int8, true, 0, 0, -1},
	{"state", sqltype.Text, true, 0, 0, -1},
	{"operation", sqltype.Text, true, 0, 0, -1},
	{"off", sqltype.Int8, true, 0, 0, -1},
	{"length", sqltype.Int8, true, 0, 0, -1},
	{"target", sqltype.Text, true, 0, 0, -1},
	{"handle_data_len", sqltype.Int2, true, 0, 0, -1},
	{"raw_result", sqltype.Int4, true, 0, 0, -1},
	{"result", sqltype.Text, true, 0, 0, -1},
	{"target_desc", sqltype.Text, true, 0, 0, -1},
	{"f_sync", sqltype.Bool, true, 0, 0, -1},
	{"f_localmem", sqltype.Bool, true, 0, 0, -1},
	{"f_buffered", sqltype.Bool, true, 0, 0, -1},
}

func captureAIOS(in Input) ([][]sqltype.Value, error) {
	if err := validateMaintenanceSource(in, aiosColumns); err != nil {
		return nil, err
	}
	if !RegisteredSource(in.Catalog, in.Source) || in.Source.Address().ObjectID != AIOSOID {
		return nil, fmt.Errorf("stuntgres: unregistered AIO source")
	}
	for i, expected := range aiosColumns {
		actual := in.Source.Columns[i]
		collation := uint32(0)
		if expected.Type == sqltype.Text {
			collation = 100
		}
		if actual.Usage.Typmod != -1 || actual.Usage.Collation.ID != collation || actual.DeclaredDimensions != 0 || !actual.Nullable || !actual.NullabilityKnown {
			return nil, fmt.Errorf("stuntgres: invalid AIO source attribute")
		}
	}
	// The logical storage engine owns no physical AIO handles. Logical SQL work
	// and native controls do not create handles; preserve a typed empty source.
	return [][]sqltype.Value{}, nil
}

var aiosViewParts = func() []builtinViewPart {
	parts := []builtinViewPart{viewText(" SELECT ")}
	for i, column := range aiosColumns {
		if i != 0 {
			parts = append(parts, viewText(",\n    "))
		}
		parts = append(parts, viewIdentifier(column.Name))
	}
	parts = append(parts, viewText("\n   FROM "), viewRoutine("pg_get_aios", ""), viewText("() "), viewIdentifier("pg_get_aios"), viewText("("))
	for i, column := range aiosColumns {
		if i != 0 {
			parts = append(parts, viewText(", "))
		}
		parts = append(parts, viewIdentifier(column.Name))
	}
	return append(parts, viewText(");"))
}()
