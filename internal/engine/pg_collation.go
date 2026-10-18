package engine

import (
	"context"
	"sort"
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/sqltype"
)

func capturePGCollation(ctx context.Context, catalog CatalogRead, ref ids.ObjectRef, source *relationDescriptor) (*querySource, error) {
	var definitions []*collationDescriptor
	for _, d := range catalog.Descriptors() {
		if c, ok := d.(*collationDescriptor); ok {
			definitions = append(definitions, c)
		}
	}
	sort.Slice(definitions, func(i, j int) bool { return definitions[i].OID < definitions[j].OID })
	rows := make([][]sqltype.Value, 0, len(definitions))
	for _, d := range definitions {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		row := make([]sqltype.Value, 12)
		owner := d.Owner
		for i, n := range []uint32{uint32(d.OID), 0, uint32(d.Namespace().ObjectID), uint32(owner.ID())} {
			if i == 1 {
				continue
			}
			v, err := sqltype.OIDValue(source.Columns[i].Usage.Type, n)
			if err != nil {
				return nil, err
			}
			row[i] = v
		}
		row[1] = sqltype.String(source.Columns[1].Usage.Type, d.Name())
		row[4] = sqltype.String(source.Columns[4].Usage.Type, d.Provider)
		row[5] = sqltype.Boolean(source.Columns[5].Usage.Type, d.Deterministic)
		v, err := sqltype.Integer(source.Columns[6].Usage.Type, int64(d.Encoding))
		if err != nil {
			return nil, err
		}
		row[6] = v
		for i, text := range []*string{d.Collate, d.CType, d.Locale, d.Rules, d.Version} {
			typ := source.Columns[7+i].Usage.Type
			if text == nil {
				row[7+i] = sqltype.Null(typ)
			} else {
				row[7+i] = sqltype.String(typ, *text)
			}
		}
		rows = append(rows, row)
	}
	return &querySource{ref: ref, anonymous: rows}, nil
}
