package engine

import (
	"context"
	"fmt"
	"sort"
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/sqltype"
)

func capturePGOperator(ctx context.Context, view CatalogRead, ref ids.ObjectRef, source *relationDescriptor) (*querySource, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	if len(source.Columns) != 15 {
		return nil, fmt.Errorf("stuntgres: invalid operator source shape")
	}
	for i, column := range source.Columns {
		id := sqltype.Oid
		switch i {
		case 1:
			id = sqltype.Name
		case 4:
			id = sqltype.Char
		case 5, 6:
			id = sqltype.Bool
		case 12, 13, 14:
			id = sqltype.Regproc
		}
		owned, ok := view.LookupType(id)
		if !ok || owned != column.Usage.Type {
			return nil, fmt.Errorf("stuntgres: foreign operator source type")
		}
	}
	var definitions []*operatorDescriptor
	for _, descriptor := range view.Descriptors() {
		d, ok := descriptor.(*operatorDescriptor)
		if !ok {
			continue
		}
		if operatorSeeded(d) && d.Function == 0 {
			continue
		}
		definitions = append(definitions, d)
	}
	sort.Slice(definitions, func(i, j int) bool { return definitions[i].Address().ObjectID < definitions[j].Address().ObjectID })
	rows := make([][]sqltype.Value, len(definitions))
	cells := make([]sqltype.Value, len(rows)*15)
	for i, identity := range definitions {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		row := cells[i*15 : (i+1)*15 : (i+1)*15]
		for j, oid := range [15]uint32{uint32(identity.Address().ObjectID), 0, uint32(identity.Namespace().ObjectID), uint32(identity.Owner.ID()), 0, 0, 0, uint32(identity.Left), uint32(identity.Right), uint32(identity.Result), uint32(identity.Commutator), uint32(identity.Negator), uint32(identity.Function), uint32(identity.Restriction), uint32(identity.Join)} {
			if j == 1 || j >= 4 && j <= 6 {
				continue
			}
			var value sqltype.Value
			var err error
			if j >= 12 {
				value, err = sqltype.RegprocValue(source.Columns[j].Usage.Type, oid)
			} else {
				value, err = sqltype.OIDValue(source.Columns[j].Usage.Type, oid)
			}
			if err != nil {
				return nil, err
			}
			row[j] = value
		}
		row[1] = sqltype.String(source.Columns[1].Usage.Type, identity.Name())
		kind := "b"
		if identity.Left == 0 {
			kind = "l"
		}
		row[4] = sqltype.String(source.Columns[4].Usage.Type, kind)
		row[5] = sqltype.Boolean(source.Columns[5].Usage.Type, identity.Merge)
		row[6] = sqltype.Boolean(source.Columns[6].Usage.Type, identity.Hash)
		rows[i] = row
	}
	return &querySource{ref: ref, anonymous: rows}, nil
}
