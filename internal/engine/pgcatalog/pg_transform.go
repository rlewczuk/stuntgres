package pgcatalog

import (
	"fmt"
	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/sqltype"
)

const TransformOID ids.OID = 3576

func capturePGTransform(in Input) ([][]sqltype.Value, error) {
	if err := in.Context.Err(); err != nil {
		return nil, err
	}
	if len(in.Source.Columns) != 5 {
		return nil, fmt.Errorf("stuntgres: invalid transform source shape")
	}
	for i, column := range in.Source.Columns {
		id := sqltype.Oid
		if i >= 3 {
			id = sqltype.Regproc
		}
		owned, ok := in.Catalog.LookupType(id)
		if !ok || owned != column.Usage.Type {
			return nil, fmt.Errorf("stuntgres: foreign transform source type")
		}
	}
	rows := make([][]sqltype.Value, 0)
	for _, descriptor := range in.Catalog.Descriptors() {
		pair, ok := descriptor.(*catalog.Transform)
		if !ok {
			continue
		}
		if err := (catalog.TransformProvider{}).Validate(in.Catalog, pair); err != nil {
			return nil, err
		}
		row := make([]sqltype.Value, 5)
		for i, id := range []ids.OID{pair.Address().ObjectID, pair.Type.ObjectID, pair.Language.ObjectID, pair.From.ObjectID, pair.To.ObjectID} {
			var value sqltype.Value
			var err error
			if i < 3 {
				value, err = sqltype.OIDValue(in.Source.Columns[i].Usage.Type, uint32(id))
			} else {
				value, err = sqltype.RegprocValue(in.Source.Columns[i].Usage.Type, uint32(id))
			}
			if err != nil {
				return nil, err
			}
			row[i] = value
		}
		rows = append(rows, row)
	}
	return rows, nil
}
