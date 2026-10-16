package engine

import (
	"context"
	"fmt"
	"stuntgres/internal/sqltype"
)

func capturePGOperator(ctx context.Context, catalog CatalogRead, ref ObjectRef, source *relationDescriptor) (*querySource, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	if len(source.columns) != 15 {
		return nil, fmt.Errorf("stuntgres: invalid operator source shape")
	}
	for i, column := range source.columns {
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
		owned, ok := catalog.LookupType(id)
		if !ok || owned != column.Usage.Type {
			return nil, fmt.Errorf("stuntgres: foreign operator source type")
		}
	}
	rows := make([][]sqltype.Value, len(supportedOperatorMetadata))
	cells := make([]sqltype.Value, len(rows)*15)
	for i := range supportedOperatorMetadata {
		fact := &supportedOperatorMetadata[i]
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		identity, ok := catalog.state.descriptors[ObjectAddress{ClassID: OperatorClassID, ObjectID: fact.oid}].(*operatorDescriptor)
		if !ok {
			return nil, fmt.Errorf("stuntgres: missing operator identity")
		}
		row := cells[i*15 : (i+1)*15 : (i+1)*15]
		for j, oid := range [15]uint32{uint32(fact.oid), 0, uint32(identity.namespace.ObjectID), 10, 0, 0, 0, uint32(identity.left), uint32(identity.right), fact.result, fact.commutator, fact.negator, fact.procedure, fact.restriction, fact.join} {
			if j == 1 || j >= 4 && j <= 6 {
				continue
			}
			var value sqltype.Value
			var err error
			if j >= 12 {
				value, err = sqltype.RegprocValue(source.columns[j].Usage.Type, oid)
			} else {
				value, err = sqltype.OIDValue(source.columns[j].Usage.Type, oid)
			}
			if err != nil {
				return nil, err
			}
			row[j] = value
		}
		row[1] = sqltype.String(source.columns[1].Usage.Type, identity.name)
		row[4] = sqltype.String(source.columns[4].Usage.Type, fact.kind)
		row[5] = sqltype.Boolean(source.columns[5].Usage.Type, fact.merge)
		row[6] = sqltype.Boolean(source.columns[6].Usage.Type, fact.hash)
		rows[i] = row
	}
	return &querySource{ref: ref, anonymous: rows}, nil
}
