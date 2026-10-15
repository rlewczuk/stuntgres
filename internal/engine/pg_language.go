package engine

import (
	"context"
	"fmt"
	"stuntgres/internal/sqltype"
)

const pgLanguageOID OID = 2612

func capturePGLanguage(ctx context.Context, catalog CatalogRead, ref ObjectRef, source *relationDescriptor) (*querySource, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	if len(source.columns) != 9 {
		return nil, fmt.Errorf("stuntgres: invalid language source shape")
	}
	for i, column := range source.columns {
		id := sqltype.Oid
		switch i {
		case 1:
			id = sqltype.Name
		case 3, 4:
			id = sqltype.Bool
		case 8:
			id = sqltype.ACLItemArray
		}
		owned, ok := catalog.LookupType(id)
		if !ok || owned != column.Usage.Type {
			return nil, fmt.Errorf("stuntgres: foreign language source type")
		}
	}
	row := make([]sqltype.Value, 9)
	for i, oid := range [8]uint32{12, 0, 10, 0, 0, 0, 0, 2246} {
		if i == 1 || i == 3 || i == 4 {
			continue
		}
		value, err := sqltype.OIDValue(source.columns[i].Usage.Type, oid)
		if err != nil {
			return nil, err
		}
		row[i] = value
	}
	row[1] = sqltype.String(source.columns[1].Usage.Type, "internal")
	row[3] = sqltype.Boolean(source.columns[3].Usage.Type, false)
	row[4] = sqltype.Boolean(source.columns[4].Usage.Type, false)
	row[8] = sqltype.Null(source.columns[8].Usage.Type)
	return &querySource{ref: ref, anonymous: [][]sqltype.Value{row}}, nil
}
