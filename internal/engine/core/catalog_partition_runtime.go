package core

import (
	"context"
	"fmt"

	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/expr"
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/engine/partition"
	"stuntgres/internal/engine/pgcatalog"
	"stuntgres/internal/engine/plan"
	"stuntgres/internal/engine/stored"
	"stuntgres/internal/parser"
	"stuntgres/internal/sqltype"
)

// PartitionMetadata reads only the immutable statement frontier, never the live
// engine or execution expressions. Failure never publishes even a partial fact.
func (r EngineCatalogRuntime) PartitionMetadata(ctx context.Context, view catalog.Read, relation *catalog.Relation, types [4]*sqltype.Descriptor) (pgcatalog.PartitionMetadataFact, error) {
	var zero pgcatalog.PartitionMetadataFact
	invalid := func() error { return fmt.Errorf("stuntgres: invalid partition metadata capture") }
	if err := ctx.Err(); err != nil {
		return zero, err
	}
	if view.Owner() == nil || view.State() == nil || relation == nil || relation.Address().ClassID != ids.RelationClassID || relation.Address().ObjectID == 0 || relation.Address().SubID != 0 || view.Get(relation.Address()) != relation || relation.RelationKind != catalog.PartitionedRelation || relation.Partition == nil {
		return zero, invalid()
	}
	for i, id := range [4]sqltype.ID{sqltype.Int2Vector, sqltype.OidVector, sqltype.OidVector, sqltype.PgNodeTree} {
		owned, ok := view.LookupType(id)
		if !ok || owned != types[i] {
			return zero, invalid()
		}
	}
	validationErr := partition.ValidateDescriptor(view, relation)
	if err := ctx.Err(); err != nil {
		return zero, err
	}
	if validationErr != nil {
		return zero, fmt.Errorf("stuntgres: invalid partition metadata capture: %v", validationErr)
	}
	_, validationErr = stored.PartitionDependRefs(ctx, view, relation)
	if err := ctx.Err(); err != nil {
		return zero, err
	}
	if validationErr != nil {
		return zero, fmt.Errorf("stuntgres: invalid partition metadata capture: %v", validationErr)
	}
	definition := relation.Partition
	fact := pgcatalog.PartitionMetadataFact{PartRelID: relation.Address().ObjectID, PartDefID: definition.DefaultChild.ObjectID, PartNatts: int16(len(definition.Keys))}
	switch definition.Strategy {
	case parser.PartitionRange:
		fact.PartStrat = "r"
	case parser.PartitionList:
		fact.PartStrat = "l"
	case parser.PartitionHash:
		fact.PartStrat = "h"
	default:
		return zero, invalid()
	}
	var attrs [32]int16
	var classes, collations [32]ids.OID
	derived := 0
	for _, key := range definition.Keys {
		if key.Derived != nil {
			derived++
		}
	}
	var expressions []expr.Expr
	if derived != 0 {
		expressions = make([]expr.Expr, 0, derived)
	}
	for i, key := range definition.Keys {
		if err := ctx.Err(); err != nil {
			return zero, err
		}
		owned, ok := view.LookupType(key.Usage.Type.ID())
		if !ok || owned != key.Usage.Type || key.Class.OID() == 0 {
			return zero, invalid()
		}
		if key.Derived == nil {
			if key.Attribute < 1 || key.Attribute > 32767 || !relation.HasAttribute(key.Attribute) {
				return zero, invalid()
			}
			attrs[i] = int16(key.Attribute)
		} else {
			if key.Derived.Definition == nil {
				return zero, invalid()
			}
			expressions = append(expressions, key.Derived.Definition)
		}
		classes[i] = ids.OID(key.Class.OID())
		collations[i] = ids.OID(key.Usage.Collation.ID)
	}
	var err error
	n := len(definition.Keys)
	fact.PartAttrs, err = sqltype.CatalogInt2Vector(types[0], attrs[:n])
	if err != nil {
		return zero, err
	}
	fact.PartClass, err = sqltype.CatalogOIDVector(types[1], classes[:n])
	if err != nil {
		return zero, err
	}
	fact.PartCollation, err = sqltype.CatalogOIDVector(types[2], collations[:n])
	if err != nil {
		return zero, err
	}
	fact.PartExprs = sqltype.Null(types[3])
	if len(expressions) != 0 {
		fact.PartExprs, err = sqltype.Tree(types[3], plan.OwnExpressionListTree(expressions))
		if err != nil {
			return zero, err
		}
	}
	if err := ctx.Err(); err != nil {
		return zero, err
	}
	return fact, nil
}
