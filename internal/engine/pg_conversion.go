package engine

import (
	"context"
	"sort"

	"stuntgres/internal/engine/ids"
	"stuntgres/internal/sqltype"
)

func capturePGConversion(ctx context.Context, catalog CatalogRead, ref ids.ObjectRef, source *relationDescriptor) (*querySource, error) {
	var definitions []*conversionDescriptor
	for _, descriptor := range catalog.Descriptors() {
		if d, ok := descriptor.(*conversionDescriptor); ok {
			definitions = append(definitions, d)
		}
	}
	sort.Slice(definitions, func(i, j int) bool { return definitions[i].oid < definitions[j].oid })
	rows := make([][]sqltype.Value, 0, len(definitions))
	for _, d := range definitions {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		row := make([]sqltype.Value, 8)
		for i, n := range [4]uint32{uint32(d.oid), 0, uint32(d.Namespace().ObjectID), uint32(d.owner.ID())} {
			if i == 1 {
				continue
			}
			value, err := sqltype.OIDValue(source.columns[i].Usage.Type, n)
			if err != nil {
				return nil, err
			}
			row[i] = value
		}
		row[1] = sqltype.String(source.columns[1].Usage.Type, d.name)
		for i, n := range [2]int{d.source, d.destination} {
			value, err := sqltype.Integer(source.columns[i+4].Usage.Type, int64(n))
			if err != nil {
				return nil, err
			}
			row[i+4] = value
		}
		value, err := sqltype.RegprocValue(source.columns[6].Usage.Type, uint32(d.handler.ObjectID))
		if err != nil {
			return nil, err
		}
		row[6] = value
		row[7] = sqltype.Boolean(source.columns[7].Usage.Type, d.defaultConversion)
		rows = append(rows, row)
	}
	return &querySource{ref: ref, anonymous: rows}, nil
}
