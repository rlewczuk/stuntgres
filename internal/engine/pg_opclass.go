package engine

import (
	"context"
	"fmt"
	"stuntgres/internal/sqltype"
)

func capturePGOpclass(ctx context.Context, catalog CatalogRead, ref ObjectRef, source *relationDescriptor) (*querySource, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	if len(source.columns) != 9 {
		return nil, fmt.Errorf("stuntgres: invalid operator class source shape")
	}
	for i, column := range source.columns {
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
	count := sqltype.IndexClassMetadataCount()
	rows := make([][]sqltype.Value, count)
	values := make([]sqltype.Value, count*9)
	for i := range count {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		fact, ok := sqltype.IndexClassMetadataAt(i)
		if !ok {
			return nil, fmt.Errorf("stuntgres: missing operator class owner metadata")
		}
		row := values[i*9 : (i+1)*9 : (i+1)*9]
		for j, oid := range [9]uint32{fact.OID, fact.Method, 0, 11, 10, fact.Family, fact.Input, 0, fact.KeyType} {
			if j == 2 || j == 7 {
				continue
			}
			value, err := sqltype.OIDValue(source.columns[j].Usage.Type, oid)
			if err != nil {
				return nil, err
			}
			row[j] = value
		}
		row[2] = sqltype.String(source.columns[2].Usage.Type, fact.Name)
		row[7] = sqltype.Boolean(source.columns[7].Usage.Type, fact.Default)
		rows[i] = row
	}
	return &querySource{ref: ref, anonymous: rows}, nil
}
