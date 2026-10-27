package pgcatalog

import (
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/engine/settings"
	"stuntgres/internal/sqltype"
)

// Reserved native relation identity, not a PostgreSQL allocator promise.
const SettingsOID ids.OID = 16202

// SettingsNameColumn is pg_settings' name column, the Input.Keep key.
const SettingsNameColumn = 0

// Capture one coherent registry-wide physical-session observation. Conversion
// runs after the settings lock is released; rows own their mutable array data.
func capturePGSettings(in Input) ([][]sqltype.Value, error) {
	ctx, source := in.Context, in.Source
	facts, err := in.Runtime.SessionSettings(ctx)
	if err != nil {
		return nil, err
	}
	capacity := len(facts)
	if in.Keep != nil {
		capacity = 0
	}
	rows := make([][]sqltype.Value, 0, capacity)
	for _, fact := range facts {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		d := fact.Definition
		name := settings.DisplayName(d.Name)
		if in.Keep != nil && !in.Keep(sqltype.String(source.Columns[SettingsNameColumn].Usage.Type, name)) {
			continue
		}
		row := make([]sqltype.Value, len(source.Columns))
		for j, column := range source.Columns {
			row[j] = sqltype.Null(column.Usage.Type)
		}
		text := func(index int, value string) {
			row[index] = sqltype.String(source.Columns[index].Usage.Type, value)
		}
		text(SettingsNameColumn, name)
		text(1, d.CurrentRaw(fact.Value))
		if d.Unit != settings.UnitNone {
			text(2, [...]string{"", "B", "kB", "MB", "ms", "s", "min", "8kB"}[d.Unit])
		}
		text(3, d.Category)
		text(4, d.Description)
		if d.ExtraDescription != "" {
			text(5, d.ExtraDescription)
		}
		text(6, [...]string{"user", "internal", "superuser", "postmaster", "sighup", "backend", "superuser-backend"}[d.Access])
		text(7, [...]string{"bool", "string", "integer", "real", "enum"}[d.Type])
		text(8, fact.Source)
		if d.Type == settings.Integer {
			text(9, d.Raw(settings.Value{Number: float64(d.IntegerMin)}))
			text(10, d.Raw(settings.Value{Number: float64(d.IntegerMax)}))
		} else if d.Type == settings.Floating {
			text(9, d.Raw(settings.Value{Number: d.FloatMin}))
			text(10, d.Raw(settings.Value{Number: d.FloatMax}))
		}
		if d.Type == settings.Enum {
			typ := source.Columns[11].Usage.Type
			elements := make([]sqltype.Value, len(d.EnumValues))
			for j, value := range d.EnumValues {
				elements[j] = sqltype.String(typ.Element(), value)
			}
			row[11], err = sqltype.OwnedArrayValue(typ, []sqltype.ArrayDimension{{Lower: 1, Length: int32(len(elements))}}, elements)
			if err != nil {
				return nil, err
			}
		}
		if !d.BootDefaultNull {
			text(12, d.Raw(d.BootValue()))
		}
		if d.NativeStartup == nil || !d.NativeStartup.ResetNull {
			text(13, d.Raw(fact.Reset))
		}
		row[16] = sqltype.Boolean(source.Columns[16].Usage.Type, false)
		rows = append(rows, row)
	}
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	return rows, nil
}
