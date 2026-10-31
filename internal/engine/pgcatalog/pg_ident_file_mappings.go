package pgcatalog

import (
	"fmt"
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/sqltype"
)

const IdentFileMappingsOID ids.OID = 12118

var identFileMappingsColumns = []ColumnSpec{
	{"map_number", sqltype.Int4, true, 0, 0, -1},
	{"file_name", sqltype.Text, true, 0, 0, -1},
	{"line_number", sqltype.Int4, true, 0, 0, -1},
	{"map_name", sqltype.Text, true, 0, 0, -1},
	{"sys_name", sqltype.Text, true, 0, 0, -1},
	{"pg_username", sqltype.Text, true, 0, 0, -1},
	{"error", sqltype.Text, true, 0, 0, -1},
}

func capturePGIdentFileMappings(in Input) ([][]sqltype.Value, error) {
	if err := validateMaintenanceSource(in, identFileMappingsColumns); err != nil {
		return nil, err
	}
	if !RegisteredSource(in.Catalog, in.Source) || in.Source.Address().ObjectID != IdentFileMappingsOID {
		return nil, fmt.Errorf("stuntgres: unregistered identity mappings source")
	}
	for i, expected := range identFileMappingsColumns {
		actual := in.Source.Columns[i]
		collation := uint32(0)
		if expected.Type == sqltype.Text {
			collation = 100
		}
		if actual.Usage.Typmod != -1 || actual.Usage.Collation.ID != collation || actual.DeclaredDimensions != 0 || !actual.Nullable || !actual.NullabilityKnown {
			return nil, fmt.Errorf("stuntgres: invalid identity mappings source attribute")
		}
	}
	facts, err := in.IdentFile.Read(in.Context)
	if err != nil {
		return nil, err
	}
	rows := make([][]sqltype.Value, 0, len(facts))
	cells := make([]sqltype.Value, len(facts)*len(identFileMappingsColumns))
	for i, fact := range facts {
		row := cells[i*7 : (i+1)*7]
		for i := range row {
			row[i] = sqltype.Null(in.Source.Columns[i].Usage.Type)
		}
		row[2], err = sqltype.Integer(in.Source.Columns[2].Usage.Type, int64(fact.Line))
		if err != nil {
			return nil, err
		}
		row[1] = sqltype.String(in.Source.Columns[1].Usage.Type, fact.File)
		if fact.Error != "" {
			row[6] = sqltype.String(in.Source.Columns[6].Usage.Type, fact.Error)
		} else {
			row[0], err = sqltype.Integer(in.Source.Columns[0].Usage.Type, int64(fact.Number))
			if err != nil {
				return nil, err
			}
			row[3] = sqltype.String(in.Source.Columns[3].Usage.Type, fact.Map)
			row[4] = sqltype.String(in.Source.Columns[4].Usage.Type, fact.System)
			row[5] = sqltype.String(in.Source.Columns[5].Usage.Type, fact.Username)
		}
		rows = append(rows, row)
	}
	if err := in.Context.Err(); err != nil {
		return nil, err
	}
	return rows, nil
}

var identFileMappingsViewParts = func() []builtinViewPart {
	parts := []builtinViewPart{viewText(" SELECT ")}
	for i, column := range identFileMappingsColumns {
		if i != 0 {
			parts = append(parts, viewText(",\n    "))
		}
		parts = append(parts, viewIdentifier(column.Name))
	}
	parts = append(parts, viewText("\n   FROM "), viewRoutine("pg_ident_file_mappings", ""), viewText("() "), viewIdentifier("a"), viewText("("))
	for i, column := range identFileMappingsColumns {
		if i != 0 {
			parts = append(parts, viewText(", "))
		}
		parts = append(parts, viewIdentifier(column.Name))
	}
	return append(parts, viewText(");"))
}()
