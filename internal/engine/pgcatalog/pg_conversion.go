package pgcatalog

import (
	"sort"

	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/sqltype"
)

func capturePGConversion(in Input) ([][]sqltype.Value, error) {
	ctx := in.Context
	view := in.Catalog
	source := in.Source
	var definitions []*catalog.Conversion
	for _, descriptor := range view.Descriptors() {
		if d, ok := descriptor.(*catalog.Conversion); ok {
			definitions = append(definitions, d)
		}
	}
	sort.Slice(definitions, func(i, j int) bool { return definitions[i].OID < definitions[j].OID })
	rows := make([][]sqltype.Value, 0, len(definitions))
	for _, d := range definitions {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		row := make([]sqltype.Value, 8)
		for i, n := range [4]uint32{uint32(d.OID), 0, uint32(d.Namespace().ObjectID), uint32(d.Owner.ID())} {
			if i == 1 {
				continue
			}
			value, err := sqltype.OIDValue(source.Columns[i].Usage.Type, n)
			if err != nil {
				return nil, err
			}
			row[i] = value
		}
		row[1] = sqltype.String(source.Columns[1].Usage.Type, d.Name())
		for i, n := range [2]int{d.Source, d.Destination} {
			value, err := sqltype.Integer(source.Columns[i+4].Usage.Type, int64(n))
			if err != nil {
				return nil, err
			}
			row[i+4] = value
		}
		value, err := sqltype.RegprocValue(source.Columns[6].Usage.Type, uint32(d.Handler.ObjectID))
		if err != nil {
			return nil, err
		}
		row[6] = value
		row[7] = sqltype.Boolean(source.Columns[7].Usage.Type, d.DefaultConversion)
		rows = append(rows, row)
	}
	return rows, nil
}
