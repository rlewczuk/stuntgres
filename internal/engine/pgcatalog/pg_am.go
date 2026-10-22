package pgcatalog

import (
	"fmt"
	"slices"
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
	count := 0
	for range view.AccessMethods() {
		count++
	}
	rows := make([][]sqltype.Value, count)
	values := make([]sqltype.Value, count*4)
	i := 0
	for fact := range view.AccessMethods() {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		row := values[i*4 : (i+1)*4 : (i+1)*4]
		var err error
		row[0], err = sqltype.OIDValue(source.Columns[0].Usage.Type, uint32(fact.Address().ObjectID))
		if err != nil {
			return nil, err
		}
		row[1] = sqltype.String(source.Columns[1].Usage.Type, fact.Name())
		row[2], err = sqltype.RegprocValue(source.Columns[2].Usage.Type, uint32(fact.Handler))
		if err != nil {
			return nil, err
		}
		row[3] = sqltype.String(source.Columns[3].Usage.Type, fact.MethodKind())
		rows[i] = row
		i++
	}
	slices.SortFunc(rows, func(a, b []sqltype.Value) int {
		left, _ := a[0].OID()
		right, _ := b[0].OID()
		if left < right {
			return -1
		}
		if left > right {
			return 1
		}
		return 0
	})
	return rows, nil
}
