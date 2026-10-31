package pgcatalog

import (
	"fmt"

	"stuntgres/internal/engine/ids"
	"stuntgres/internal/sqltype"
)

const FileSettingsOID ids.OID = 12110

var fileSettingsColumns = []ColumnSpec{
	{"sourcefile", sqltype.Text, true, 0, 0, -1},
	{"sourceline", sqltype.Int4, true, 0, 0, -1},
	{"seqno", sqltype.Int4, true, 0, 0, -1},
	{"name", sqltype.Text, true, 0, 0, -1},
	{"setting", sqltype.Text, true, 0, 0, -1},
	{"applied", sqltype.Bool, true, 0, 0, -1},
	{"error", sqltype.Text, true, 0, 0, -1},
}

func capturePGFileSettings(in Input) ([][]sqltype.Value, error) {
	if err := validateMaintenanceSource(in, fileSettingsColumns); err != nil {
		return nil, err
	}
	if !RegisteredSource(in.Catalog, in.Source) || in.Source.Address().ObjectID != FileSettingsOID {
		return nil, fmt.Errorf("stuntgres: unregistered file settings source")
	}
	for i, expected := range fileSettingsColumns {
		actual := in.Source.Columns[i]
		collation := uint32(0)
		if expected.Type == sqltype.Text {
			collation = 100
		}
		if actual.Usage.Typmod != -1 || actual.Usage.Collation.ID != collation || actual.DeclaredDimensions != 0 || !actual.Nullable || !actual.NullabilityKnown {
			return nil, fmt.Errorf("stuntgres: invalid file settings source attribute")
		}
	}
	// There is no parsed GUC-file source. Settings defaults, SET/RESET, and
	// process CLI configuration are not file assignments or file diagnostics.
	return nil, in.Context.Err()
}

var fileSettingsViewParts = func() []builtinViewPart {
	parts := []builtinViewPart{viewText(" SELECT ")}
	for i, column := range fileSettingsColumns {
		if i != 0 {
			parts = append(parts, viewText(",\n    "))
		}
		parts = append(parts, viewIdentifier(column.Name))
	}
	parts = append(parts, viewText("\n   FROM "), viewRoutine("pg_show_all_file_settings", ""), viewText("() "), viewIdentifier("a"), viewText("("))
	for i, column := range fileSettingsColumns {
		if i != 0 {
			parts = append(parts, viewText(", "))
		}
		parts = append(parts, viewIdentifier(column.Name))
	}
	return append(parts, viewText(");"))
}()
