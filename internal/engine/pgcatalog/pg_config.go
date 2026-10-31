package pgcatalog

import (
	"fmt"

	"stuntgres/internal/engine/functions"
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/sqltype"
)

const ConfigOID ids.OID = 12130

var configColumns = []ColumnSpec{
	{"name", sqltype.Text, true, 0, 0, -1},
	{"setting", sqltype.Text, true, 0, 0, -1},
}

func capturePGConfig(in Input) ([][]sqltype.Value, error) {
	if err := validateMaintenanceSource(in, configColumns); err != nil {
		return nil, err
	}
	if !RegisteredSource(in.Catalog, in.Source) || in.Source.Address().ObjectID != ConfigOID {
		return nil, fmt.Errorf("stuntgres: unregistered configuration source")
	}
	for _, column := range in.Source.Columns {
		if column.Usage.Typmod != -1 || column.Usage.Collation.ID != 100 || column.DeclaredDimensions != 0 || !column.Nullable || !column.NullabilityKnown {
			return nil, fmt.Errorf("stuntgres: invalid configuration source attribute")
		}
	}
	if err := in.Context.Err(); err != nil {
		return nil, err
	}
	// The embedded package owns a compatibility identity, not a C installation.
	// Unsupported PostgreSQL installation/build keys are absent, not placeholders.
	return [][]sqltype.Value{{
		sqltype.String(in.Source.Columns[0].Usage.Type, "VERSION"),
		sqltype.String(in.Source.Columns[1].Usage.Type, functions.EmbeddedVersionBanner),
	}}, nil
}

var configViewParts = []builtinViewPart{
	viewText(" SELECT "), viewIdentifier("name"), viewText(",\n    "), viewIdentifier("setting"),
	viewText("\n   FROM "), viewRoutine("pg_config", ""), viewText("() "), viewIdentifier("pg_config"),
	viewText("("), viewIdentifier("name"), viewText(", "), viewIdentifier("setting"), viewText(");"),
}
