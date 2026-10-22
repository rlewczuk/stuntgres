package engine

import (
	"context"
	"fmt"
	"strconv"

	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/expr"
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/sqltype"
)

// partitionInquiryRelation distinguishes kind from membership: a childless
// partitioned relation is not a leaf; an unattached local physical index is not
// a partition merely because its indexed table is one.
func partitionInquiryRelation(view catalog.Read, address ids.ObjectAddress) (*catalog.Relation, ids.ObjectAddress, bool, bool) {
	relation, ok := view.Get(address).(*catalog.Relation)
	if !ok {
		return nil, ids.ObjectAddress{}, false, false
	}
	if relation.SQLIndex != nil {
		indexed, ok := view.Get(relation.SQLIndex.Parent).(*catalog.Relation)
		partitioned := ok && indexed.Partition != nil
		parent := relation.SQLIndex.PartitionParent
		return relation, parent, partitioned, partitioned || parent.ObjectID != 0
	}
	parent := ids.ObjectAddress{}
	if relation.Membership != nil {
		parent = relation.Membership.Parent
	}
	partitioned := relation.Partition != nil
	return relation, parent, partitioned, partitioned || parent.ObjectID != 0
}

func partitionInquiryResult(view catalog.Read, result *sqltype.Descriptor, tree bool) error {
	if result == nil {
		return fmt.Errorf("stuntgres: missing partition inquiry type")
	}
	if !tree {
		owned, ok := view.LookupType(sqltype.Regclass)
		if !ok || result != owned {
			return fmt.Errorf("stuntgres: foreign partition inquiry type")
		}
		return nil
	}
	if !result.IsComposite() || result.CompositeLen() != 4 {
		return fmt.Errorf("stuntgres: invalid partition tree shape")
	}
	for i, id := range [...]sqltype.ID{sqltype.Regclass, sqltype.Regclass, sqltype.Bool, sqltype.Int4} {
		owned, ok := view.LookupType(id)
		if !ok || result.CompositeMember(i).Usage.Type != owned {
			return fmt.Errorf("stuntgres: foreign partition tree field type")
		}
	}
	return nil
}

func (h *statementHost) PartitionTree(oid uint32, result *sqltype.Descriptor) (expr.SourceCursor, error) {
	ctx := h.scope().op.Context()
	if err := ctx.Err(); err != nil {
		return expr.SourceCursor{}, err
	}
	view, err := h.catalogInquiryCatalog()
	if err != nil {
		return expr.SourceCursor{}, err
	}
	values, err := partitionTreeValues(ctx, view, oid, result)
	if err != nil {
		return expr.SourceCursor{}, err
	}
	return expr.OwnedSource(result, values), nil
}
func (h *statementHost) PartitionAncestors(oid uint32, result *sqltype.Descriptor) (expr.SourceCursor, error) {
	ctx := h.scope().op.Context()
	if err := ctx.Err(); err != nil {
		return expr.SourceCursor{}, err
	}
	view, err := h.catalogInquiryCatalog()
	if err != nil {
		return expr.SourceCursor{}, err
	}
	values, err := partitionAncestorValues(ctx, view, oid, result)
	if err != nil {
		return expr.SourceCursor{}, err
	}
	return expr.OwnedSource(result, values), nil
}
func (h *statementHost) PartitionRoot(oid uint32, result *sqltype.Descriptor) (sqltype.Value, error) {
	ctx := h.scope().op.Context()
	if err := ctx.Err(); err != nil {
		return sqltype.Value{}, err
	}
	view, err := h.catalogInquiryCatalog()
	if err != nil {
		return sqltype.Value{}, err
	}
	return partitionRootValue(ctx, view, oid, result)
}

func partitionTreeValues(ctx context.Context, view catalog.Read, oid uint32, result *sqltype.Descriptor) ([]sqltype.Value, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	if err := partitionInquiryResult(view, result, true); err != nil {
		return nil, err
	}
	address := ids.ObjectAddress{ClassID: ids.RelationClassID, ObjectID: ids.OID(oid)}
	root, _, _, eligible := partitionInquiryRelation(view, address)
	if !eligible {
		return nil, nil
	}
	// Index children come only from actual index attachment edges. Build one
	// invocation-local adjacency map, not one full catalog scan per descendant.
	var indexChildren map[ids.ObjectAddress][]ids.ObjectAddress
	if root.SQLIndex != nil {
		indexChildren = make(map[ids.ObjectAddress][]ids.ObjectAddress)
		for _, descriptor := range view.Descriptors() {
			if err := ctx.Err(); err != nil {
				return nil, err
			}
			if r, ok := descriptor.(*catalog.Relation); ok && r.SQLIndex != nil && r.SQLIndex.PartitionParent.ObjectID != 0 {
				parent := r.SQLIndex.PartitionParent
				indexChildren[parent] = append(indexChildren[parent], r.Address())
			}
		}
	}
	type entry struct {
		address ids.ObjectAddress
		level   int64
	}
	var small [16]entry
	pending := append(small[:0], entry{address, 0})
	var values []sqltype.Value
	for len(pending) > 0 {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		at := pending[len(pending)-1]
		pending = pending[:len(pending)-1]
		r, parent, partitioned, eligible := partitionInquiryRelation(view, at.address)
		if !eligible {
			return nil, fmt.Errorf("stuntgres: invalid captured partition edge")
		}
		fields := make([]sqltype.Value, 4)
		var err error
		fields[0] = partitionRegclassValue(result.CompositeMember(0).Usage.Type, r.Address().ObjectID)
		fields[1] = sqltype.Null(result.CompositeMember(1).Usage.Type)
		if parent.ObjectID != 0 {
			fields[1] = partitionRegclassValue(result.CompositeMember(1).Usage.Type, parent.ObjectID)
		}
		fields[2] = sqltype.Boolean(result.CompositeMember(2).Usage.Type, !partitioned)
		fields[3], err = sqltype.Integer(result.CompositeMember(3).Usage.Type, at.level)
		if err != nil {
			return nil, err
		}
		value, err := sqltype.OwnedCompositeValue(result, fields)
		if err != nil {
			return nil, err
		}
		values = append(values, value)
		var children []ids.ObjectAddress
		if indexChildren != nil {
			children = indexChildren[at.address]
		} else if r.Partition != nil {
			children = r.Partition.Children
		}
		for _, child := range children {
			pending = append(pending, entry{child, at.level + 1})
		}
	}
	return values, ctx.Err()
}

func partitionAncestorValues(ctx context.Context, view catalog.Read, oid uint32, result *sqltype.Descriptor) ([]sqltype.Value, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	if err := partitionInquiryResult(view, result, false); err != nil {
		return nil, err
	}
	address := ids.ObjectAddress{ClassID: ids.RelationClassID, ObjectID: ids.OID(oid)}
	var values []sqltype.Value
	for {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		r, parent, _, eligible := partitionInquiryRelation(view, address)
		if !eligible {
			if len(values) != 0 {
				return nil, fmt.Errorf("stuntgres: invalid captured partition parent")
			}
			return nil, nil
		}
		value := partitionRegclassValue(result, r.Address().ObjectID)
		values = append(values, value)
		if parent.ObjectID == 0 {
			return values, nil
		}
		address = parent
	}
}

func partitionRootValue(ctx context.Context, view catalog.Read, oid uint32, result *sqltype.Descriptor) (sqltype.Value, error) {
	if err := ctx.Err(); err != nil {
		return sqltype.Value{}, err
	}
	if err := partitionInquiryResult(view, result, false); err != nil {
		return sqltype.Value{}, err
	}
	address := ids.ObjectAddress{ClassID: ids.RelationClassID, ObjectID: ids.OID(oid)}
	first := true
	for {
		if err := ctx.Err(); err != nil {
			return sqltype.Value{}, err
		}
		r, parent, _, eligible := partitionInquiryRelation(view, address)
		if !eligible {
			if !first {
				return sqltype.Value{}, fmt.Errorf("stuntgres: invalid captured partition parent")
			}
			return sqltype.Null(result), nil
		}
		if parent.ObjectID == 0 {
			return partitionRegclassValue(result, r.Address().ObjectID), nil
		}
		first = false
		address = parent
	}
}

func partitionRegclassValue(result *sqltype.Descriptor, oid ids.OID) sqltype.Value {
	return sqltype.String(result, strconv.FormatUint(uint64(oid), 10))
}
