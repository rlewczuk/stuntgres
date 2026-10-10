package engine

import (
	"context"
	"sort"
	"stuntgres/internal/sqltype"
)

func capturePGCollation(ctx context.Context, catalog CatalogRead, ref ObjectRef, source *relationDescriptor) (*querySource, error) {
	var definitions []*collationDescriptor
	for _, d := range catalog.state.descriptors {
		if c, ok := d.(*collationDescriptor); ok {
			definitions = append(definitions, c)
		}
	}
	sort.Slice(definitions, func(i, j int) bool { return definitions[i].oid < definitions[j].oid })
	rows := make([][]sqltype.Value, 0, len(definitions))
	for _, d := range definitions {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		row := make([]sqltype.Value, 12)
		owner := d.owner
		for i, n := range []uint32{uint32(d.oid), 0, uint32(d.Namespace().ObjectID), uint32(owner.id)} {
			if i == 1 {
				continue
			}
			v, err := sqltype.OIDValue(source.columns[i].Usage.Type, n)
			if err != nil {
				return nil, err
			}
			row[i] = v
		}
		row[1] = sqltype.String(source.columns[1].Usage.Type, d.name)
		row[4] = sqltype.String(source.columns[4].Usage.Type, d.provider)
		row[5] = sqltype.Boolean(source.columns[5].Usage.Type, d.deterministic)
		v, err := sqltype.Integer(source.columns[6].Usage.Type, int64(d.encoding))
		if err != nil {
			return nil, err
		}
		row[6] = v
		for i, text := range []*string{d.collate, d.ctype, d.locale, d.rules, d.version} {
			typ := source.columns[7+i].Usage.Type
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
