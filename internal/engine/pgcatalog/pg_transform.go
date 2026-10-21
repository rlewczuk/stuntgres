package pgcatalog

import (
	"fmt"
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
	// Casts, codecs and SQL routines are not procedural-language transforms.
	// A nonnil empty slice identifies an anonymous source to the executor.
	return [][]sqltype.Value{}, nil
}
