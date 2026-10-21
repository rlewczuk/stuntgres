package pgcatalog

import (
	"fmt"
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/sqltype"
)

const TSConfigOID ids.OID = 3602

func capturePGTSConfig(in Input) ([][]sqltype.Value, error) {
	if err := in.Context.Err(); err != nil {
		return nil, err
	}
	if len(in.Source.Columns) != 5 {
		return nil, fmt.Errorf("stuntgres: invalid text search configuration source shape")
	}
	for i, column := range in.Source.Columns {
		id := sqltype.Oid
		if i == 1 {
			id = sqltype.Name
		}
		owned, ok := in.Catalog.LookupType(id)
		if !ok || owned != column.Usage.Type {
			return nil, fmt.Errorf("stuntgres: foreign text search configuration source type")
		}
	}
	// Scalar FTS codecs and installed dictionaries do not provide configurations.
	// A nonnil empty slice identifies an anonymous source to the executor.
	return [][]sqltype.Value{}, nil
}
