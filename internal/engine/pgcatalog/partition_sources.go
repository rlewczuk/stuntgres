package pgcatalog

import (
	"fmt"
	"sort"

	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/sqltype"
)

func capturePGPartitionedTable(in Input) ([][]sqltype.Value, error) {
	ctx, view, source := in.Context, in.Catalog, in.Source
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	if in.Owner == nil || view.Owner() != in.Owner || view.State() == nil || source == nil || in.Runtime == nil || source.Address() != (ids.ObjectAddress{ClassID: ids.RelationClassID, ObjectID: PartitionedTableOID}) || in.Ref.Domain() != in.Owner.Domain || in.Ref.Epoch != view.Epoch() || in.Ref.Address != source.Address() || view.Get(source.Address()) != source {
		return nil, fmt.Errorf("stuntgres: invalid partition source capture")
	}
	expected := [8]sqltype.ID{sqltype.Oid, sqltype.Char, sqltype.Int2, sqltype.Oid, sqltype.Int2Vector, sqltype.OidVector, sqltype.OidVector, sqltype.PgNodeTree}
	if len(source.Columns) != len(expected) {
		return nil, fmt.Errorf("stuntgres: invalid partition source shape")
	}
	for i, id := range expected {
		owned, ok := view.LookupType(id)
		if !ok || owned != source.Columns[i].Usage.Type {
			return nil, fmt.Errorf("stuntgres: foreign partition source type")
		}
	}
	var relations []*catalog.Relation
	for _, descriptor := range view.Descriptors() {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		relation, ok := descriptor.(*catalog.Relation)
		if !ok {
			continue
		}
		if (relation.RelationKind == catalog.PartitionedRelation) != (relation.Partition != nil) {
			return nil, fmt.Errorf("stuntgres: invalid partition source descriptor")
		}
		if relation.Partition != nil {
			relations = append(relations, relation)
		}
	}
	sort.Slice(relations, func(i, j int) bool { return relations[i].Address().ObjectID < relations[j].Address().ObjectID })
	rows := make([][]sqltype.Value, 0, len(relations))
	for _, relation := range relations {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		fact, err := in.Runtime.PartitionMetadata(ctx, view, relation, [4]*sqltype.Descriptor{source.Columns[4].Usage.Type, source.Columns[5].Usage.Type, source.Columns[6].Usage.Type, source.Columns[7].Usage.Type})
		if err != nil {
			return nil, err
		}
		relid, err := sqltype.OIDValue(source.Columns[0].Usage.Type, uint32(fact.PartRelID))
		if err != nil {
			return nil, err
		}
		natts, err := sqltype.Integer(source.Columns[2].Usage.Type, int64(fact.PartNatts))
		if err != nil {
			return nil, err
		}
		defaultID, err := sqltype.OIDValue(source.Columns[3].Usage.Type, uint32(fact.PartDefID))
		if err != nil {
			return nil, err
		}
		rows = append(rows, []sqltype.Value{relid, sqltype.String(source.Columns[1].Usage.Type, fact.PartStrat), natts, defaultID, fact.PartAttrs, fact.PartClass, fact.PartCollation, fact.PartExprs})
	}
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	return rows, nil
}
