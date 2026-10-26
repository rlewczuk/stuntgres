package pgcatalog

import (
	"fmt"
	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/sqltype"
)

func capturePGEventTrigger(in Input) ([][]sqltype.Value, error) {
	if len(in.Source.Columns) != 7 {
		return nil, fmt.Errorf("stuntgres: invalid event trigger source shape")
	}
	for i, id := range [...]sqltype.ID{sqltype.Oid, sqltype.Name, sqltype.Name, sqltype.Oid, sqltype.Oid, sqltype.Char, 1009} {
		owned, ok := in.Catalog.LookupType(id)
		if !ok || owned != in.Source.Columns[i].Usage.Type {
			return nil, fmt.Errorf("stuntgres: foreign event trigger source type")
		}
	}
	var rows [][]sqltype.Value
	for _, d := range in.Catalog.EventTriggers() {
		if err := in.Context.Err(); err != nil {
			return nil, err
		}
		if err := (catalog.EventTriggerProvider{}).Validate(in.Catalog, d); err != nil {
			return nil, err
		}
		if err := in.Runtime.ValidateACL(in.Roles, d); err != nil {
			return nil, err
		}
		row := make([]sqltype.Value, 7)
		for _, field := range []struct {
			slot int
			oid  uint32
		}{{0, uint32(d.Address().ObjectID)}, {3, uint32(d.Owner.ID())}, {4, uint32(d.Function.Address.ObjectID)}} {
			var err error
			row[field.slot], err = sqltype.OIDValue(in.Source.Columns[field.slot].Usage.Type, field.oid)
			if err != nil {
				return nil, err
			}
		}
		row[1] = sqltype.String(in.Source.Columns[1].Usage.Type, d.Name())
		row[2] = sqltype.String(in.Source.Columns[2].Usage.Type, d.Event)
		state := "D"
		if d.Enabled {
			state = "O"
		}
		row[5] = sqltype.String(in.Source.Columns[5].Usage.Type, state)
		array := in.Source.Columns[6].Usage.Type
		row[6] = sqltype.Null(array)
		if d.Tags != nil {
			values := make([]sqltype.Value, len(d.Tags))
			for i, tag := range d.Tags {
				values[i] = sqltype.String(array.Element(), tag)
			}
			var err error
			row[6], err = sqltype.OwnedArrayValue(array, []sqltype.ArrayDimension{{Length: int32(len(values)), Lower: 1}}, values)
			if err != nil {
				return nil, err
			}
		}
		rows = append(rows, row)
	}
	return rows, in.Context.Err()
}
