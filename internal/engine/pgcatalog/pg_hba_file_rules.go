package pgcatalog

import (
	"fmt"
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/sqltype"
)

const HBAFileRulesOID ids.OID = 12114

var hbaFileRulesColumns = []ColumnSpec{
	{"rule_number", sqltype.Int4, true, 0, 0, -1},
	{"file_name", sqltype.Text, true, 0, 0, -1},
	{"line_number", sqltype.Int4, true, 0, 0, -1},
	{"type", sqltype.Text, true, 0, 0, -1},
	{"database", sqltype.ID(1009), true, 0, 0, -1},
	{"user_name", sqltype.ID(1009), true, 0, 0, -1},
	{"address", sqltype.Text, true, 0, 0, -1},
	{"netmask", sqltype.Text, true, 0, 0, -1},
	{"auth_method", sqltype.Text, true, 0, 0, -1},
	{"options", sqltype.ID(1009), true, 0, 0, -1},
	{"error", sqltype.Text, true, 0, 0, -1},
}

func capturePGHBAFileRules(in Input) ([][]sqltype.Value, error) {
	if err := validateMaintenanceSource(in, hbaFileRulesColumns); err != nil {
		return nil, err
	}
	if !RegisteredSource(in.Catalog, in.Source) || in.Source.Address().ObjectID != HBAFileRulesOID {
		return nil, fmt.Errorf("stuntgres: unregistered HBA rules source")
	}
	for i, expected := range hbaFileRulesColumns {
		actual := in.Source.Columns[i]
		collation := uint32(100)
		if expected.Type == sqltype.Int4 {
			collation = 0
		}
		if actual.Usage.Typmod != -1 || actual.Usage.Collation.ID != collation || actual.DeclaredDimensions != 0 || !actual.Nullable || !actual.NullabilityKnown {
			return nil, fmt.Errorf("stuntgres: invalid HBA rules source attribute")
		}
		if expected.Type == 1009 && (actual.Usage.Type.Element() == nil || actual.Usage.Type.Element().ID() != sqltype.Text) {
			return nil, fmt.Errorf("stuntgres: invalid HBA rules array element")
		}
	}
	facts := in.HBARules.Rules()
	rows := make([][]sqltype.Value, len(facts))
	cells := make([]sqltype.Value, len(facts)*11)
	for i, fact := range facts {
		if err := in.Context.Err(); err != nil {
			return nil, err
		}
		row := cells[i*11 : (i+1)*11]
		rows[i] = row
		for j := range row {
			row[j] = sqltype.Null(in.Source.Columns[j].Usage.Type)
		}
		for _, scalar := range []struct {
			index int
			value *int32
		}{{0, fact.RuleNumber}, {2, fact.LineNumber}} {
			if scalar.value != nil {
				value, err := sqltype.Integer(in.Source.Columns[scalar.index].Usage.Type, int64(*scalar.value))
				if err != nil {
					return nil, err
				}
				row[scalar.index] = value
			}
		}
		for _, scalar := range []struct {
			index int
			value *string
		}{{1, fact.FileName}, {3, fact.Type}, {6, fact.Address}, {7, fact.Netmask}, {8, fact.AuthMethod}, {10, fact.Error}} {
			if scalar.value != nil {
				row[scalar.index] = sqltype.String(in.Source.Columns[scalar.index].Usage.Type, *scalar.value)
			}
		}
		for _, array := range []struct {
			index  int
			values []string
		}{{4, fact.Database}, {5, fact.UserName}, {9, fact.Options}} {
			if array.values == nil {
				continue
			}
			typ := in.Source.Columns[array.index].Usage.Type
			var dimensions []sqltype.ArrayDimension
			var elements []sqltype.Value
			if len(array.values) != 0 {
				dimensions = []sqltype.ArrayDimension{{Lower: 1, Length: int32(len(array.values))}}
				elements = make([]sqltype.Value, len(array.values))
				for k, text := range array.values {
					elements[k] = sqltype.String(typ.Element(), text)
				}
			}
			value, err := sqltype.OwnedArrayValue(typ, dimensions, elements)
			if err != nil {
				return nil, err
			}
			row[array.index] = value
		}
	}
	if err := in.Context.Err(); err != nil {
		return nil, err
	}
	return rows, nil
}

var hbaFileRulesViewParts = func() []builtinViewPart {
	parts := []builtinViewPart{viewText(" SELECT ")}
	for i, column := range hbaFileRulesColumns {
		if i != 0 {
			parts = append(parts, viewText(",\n    "))
		}
		parts = append(parts, viewIdentifier(column.Name))
	}
	parts = append(parts, viewText("\n   FROM "), viewRoutine("pg_hba_file_rules", ""), viewText("() "), viewIdentifier("a"), viewText("("))
	for i, column := range hbaFileRulesColumns {
		if i != 0 {
			parts = append(parts, viewText(", "))
		}
		parts = append(parts, viewIdentifier(column.Name))
	}
	return append(parts, viewText(");"))
}()
