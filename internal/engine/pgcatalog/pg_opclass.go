package pgcatalog

import (
	"fmt"
	"stuntgres/internal/sqltype"
)

func capturePGOpclass(in Input) ([][]sqltype.Value, error) {
	ctx := in.Context
	catalog := in.Catalog
	source := in.Source
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	if len(source.Columns) != 9 {
		return nil, fmt.Errorf("stuntgres: invalid operator class source shape")
	}
	for i, column := range source.Columns {
		id := sqltype.Oid
		if i == 2 {
			id = sqltype.Name
		}
		if i == 7 {
			id = sqltype.Bool
		}
		owned, ok := catalog.LookupType(id)
		if !ok || owned != column.Usage.Type {
			return nil, fmt.Errorf("stuntgres: foreign operator class source type")
		}
	}
	classes := catalog.OperatorClasses()
	count := len(classes)
	rows := make([][]sqltype.Value, count)
	values := make([]sqltype.Value, count*9)
	for i := range count {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		fact := classes[i]
		row := values[i*9 : (i+1)*9 : (i+1)*9]
		for j, oid := range [9]uint32{uint32(fact.Address().ObjectID), uint32(fact.Method), 0, uint32(fact.Namespace().ObjectID), uint32(fact.Owner.ID()), uint32(fact.Family), uint32(fact.InputType), 0, uint32(fact.StorageType)} {
			if j == 2 || j == 7 {
				continue
			}
			value, err := sqltype.OIDValue(source.Columns[j].Usage.Type, oid)
			if err != nil {
				return nil, err
			}
			row[j] = value
		}
		row[2] = sqltype.String(source.Columns[2].Usage.Type, fact.Name())
		row[7] = sqltype.Boolean(source.Columns[7].Usage.Type, fact.Default)
		rows[i] = row
	}
	return rows, nil
}
