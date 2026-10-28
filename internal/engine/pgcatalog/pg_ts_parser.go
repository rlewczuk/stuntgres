package pgcatalog

import (
	"fmt"

	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/sqltype"
)

const TSParserOID ids.OID = 3601

func capturePGTSParser(in Input) ([][]sqltype.Value, error) {
	if err := in.Context.Err(); err != nil {
		return nil, err
	}
	if len(in.Source.Columns) != 8 {
		return nil, fmt.Errorf("stuntgres: invalid parser source shape")
	}
	for i, c := range in.Source.Columns {
		id := sqltype.Regproc
		if i == 0 || i == 2 {
			id = sqltype.Oid
		} else if i == 1 {
			id = sqltype.Name
		}
		owned, ok := in.Catalog.LookupType(id)
		if !ok || owned != c.Usage.Type {
			return nil, fmt.Errorf("stuntgres: foreign parser source type")
		}
	}
	var rows [][]sqltype.Value
	for _, raw := range in.Catalog.Descriptors() {
		if err := in.Context.Err(); err != nil {
			return nil, err
		}
		p, ok := raw.(*catalog.TSParser)
		if !ok {
			continue
		}
		if err := (catalog.TSParserProvider{}).Validate(in.Catalog, p); err != nil {
			return nil, err
		}
		row := make([]sqltype.Value, 8)
		values := [8]ids.OID{p.Address().ObjectID, 0, p.Namespace().ObjectID, p.Start, p.Token, p.End, p.Headline, p.LexType}
		for i, n := range values {
			d := in.Source.Columns[i].Usage.Type
			var err error
			if i == 1 {
				row[i] = sqltype.String(d, p.Name())
			} else if i == 0 || i == 2 {
				row[i], err = sqltype.OIDValue(d, uint32(n))
			} else {
				row[i], err = sqltype.RegprocValue(d, uint32(n))
			}
			if err != nil {
				return nil, err
			}
		}
		rows = append(rows, row)
	}
	return rows, nil
}
