package pgcatalog

import (
	"fmt"
	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/sqltype"
)

const AMOID ids.OID = 2601

func capturePGAM(in Input) ([][]sqltype.Value, error) {
	ctx := in.Context
	view := in.Catalog
	source := in.Source
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	if source == nil || len(source.Columns) != 4 {
		return nil, fmt.Errorf("stuntgres: invalid access-method source shape")
	}
	for i, id := range [...]sqltype.ID{sqltype.Oid, sqltype.Name, sqltype.Regproc, sqltype.Char} {
		owned, ok := view.LookupType(id)
		if !ok || owned != source.Columns[i].Usage.Type {
			return nil, fmt.Errorf("stuntgres: foreign access-method source type")
		}
	}
	methods := catalog.AccessMethods()
	rows := make([][]sqltype.Value, len(methods))
	values := make([]sqltype.Value, len(methods)*4)
	for i, fact := range methods {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		row := values[i*4 : (i+1)*4 : (i+1)*4]
		var err error
		row[0], err = sqltype.OIDValue(source.Columns[0].Usage.Type, uint32(fact.OID))
		if err != nil {
			return nil, err
		}
		row[1] = sqltype.String(source.Columns[1].Usage.Type, fact.Name)
		row[2], err = sqltype.RegprocValue(source.Columns[2].Usage.Type, uint32(fact.Handler))
		if err != nil {
			return nil, err
		}
		row[3] = sqltype.String(source.Columns[3].Usage.Type, fact.Kind)
		rows[i] = row
	}
	return rows, nil
}
