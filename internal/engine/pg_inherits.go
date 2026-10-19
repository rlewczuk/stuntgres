package engine

import (
	"context"
	"fmt"
	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/sqltype"
)

// Hierarchy owners publish these edges with their descriptors. Capture only the
// caller's immutable generation, including its transaction-private overlay.
func capturePGInherits(ctx context.Context, view catalog.Read, ref ids.ObjectRef, source *catalog.Relation) (*querySource, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	if len(source.Columns) != 4 {
		return nil, fmt.Errorf("stuntgres: invalid inheritance source shape")
	}
	var types [4]*sqltype.Descriptor
	for i, id := range [...]sqltype.ID{sqltype.Oid, sqltype.Oid, sqltype.Int4, sqltype.Bool} {
		owned, ok := view.LookupType(id)
		if !ok || owned != source.Columns[i].Usage.Type {
			return nil, fmt.Errorf("stuntgres: foreign inheritance source type")
		}
		types[i] = owned
	}
	count := 0
	for _, descriptor := range view.Descriptors() {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		if relation, ok := descriptor.(*catalog.Relation); ok {
			if relation.Inheritance != nil {
				count += len(relation.Inheritance.Parents)
			}
			if relation.Membership != nil {
				count++
			}
			if relation.SQLIndex != nil && relation.SQLIndex.PartitionParent.ObjectID != 0 {
				count++
			}
		}
	}
	rows := make([][]sqltype.Value, count)
	values := make([]sqltype.Value, count*4)
	at := 0
	emit := func(child, parent ids.OID, ordinal int64) error {
		if err := ctx.Err(); err != nil {
			return err
		}
		row := values[at*4 : (at+1)*4 : (at+1)*4]
		var err error
		row[0], err = sqltype.OIDValue(types[0], uint32(child))
		if err != nil {
			return err
		}
		row[1], err = sqltype.OIDValue(types[1], uint32(parent))
		if err != nil {
			return err
		}
		row[2], err = sqltype.Integer(types[2], ordinal)
		if err != nil {
			return err
		}
		row[3] = sqltype.Boolean(types[3], false)
		rows[at] = row
		at++
		return nil
	}
	for _, descriptor := range view.Descriptors() {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		relation, ok := descriptor.(*catalog.Relation)
		if !ok {
			continue
		}
		if relation.Inheritance != nil {
			for i, parent := range relation.Inheritance.Parents {
				if err := emit(relation.Address().ObjectID, parent.ObjectID, int64(i)+1); err != nil {
					return nil, err
				}
			}
		}
		if relation.Membership != nil {
			if err := emit(relation.Address().ObjectID, relation.Membership.Parent.ObjectID, 1); err != nil {
				return nil, err
			}
		}
		// SQLIndex.Parent identifies the indexed table, not an inheritance edge.
		if relation.SQLIndex != nil && relation.SQLIndex.PartitionParent.ObjectID != 0 {
			if err := emit(relation.Address().ObjectID, relation.SQLIndex.PartitionParent.ObjectID, 1); err != nil {
				return nil, err
			}
		}
	}
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	return &querySource{ref: ref, anonymous: rows}, nil
}
