package access

import (
	"fmt"

	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/engine/sepgsql"
)

// SepgsqlAttributePostAlter consumes the actual stable attribute number emitted
// by a supported semantic owner. Dropped attributes retain genuine catalog
// identity and share the catalog's tombstone naming authority.
func (v View) SepgsqlAttributePostAlter(relation *catalog.Relation, address ids.ObjectAddress) error {
	return v.sepgsqlAttributeDemand(relation, address, sepgsql.Setattr)
}

// SepgsqlAttributeDrop belongs to an actual accepted column-retirement owner.
func (v View) SepgsqlAttributeDrop(relation *catalog.Relation, address ids.ObjectAddress) error {
	return v.sepgsqlAttributeDemand(relation, address, sepgsql.Drop)
}

func (v View) sepgsqlAttributeDemand(relation *catalog.Relation, address ids.ObjectAddress, mask sepgsql.Mask) error {
	if v.Sepgsql.Policy == nil {
		return nil
	}
	parent := address
	parent.SubID = 0
	raw, err := v.Object(v.Ref(parent))
	if err != nil {
		return err
	}
	current, ok := raw.(*catalog.Relation)
	if !ok || relation == nil || current.Address() != relation.Address() || current.RelationKind != relation.RelationKind {
		return ids.ErrCatalogReplaced
	}
	if relation.RelationKind != catalog.TableRelation && relation.RelationKind != catalog.PartitionedRelation && (relation.RelationKind != catalog.CatalogRelation || relation.CatalogView) {
		return nil
	}
	name := catalog.SepgsqlAttributeName(relation, address.SubID)
	if name == "" {
		return sqlError("XX000", fmt.Sprintf("cache lookup failed for attribute %d of relation %d", address.SubID, uint32(parent.ObjectID)))
	}
	identity := catalog.SepgsqlObjectKey(v.Catalog, relation, 0) + "." + catalog.DependencyIdentifier(name)
	if _, err = v.SepgsqlDecision(v.SepgsqlContext(address), "db_column", identity, mask, true); err != nil {
		return err
	}
	return v.observeSepgsql(address, "db_column", mask)
}

// SepgsqlRelationPostAlter reads policy/labels from the genuine authorization
// root and only presentation from the semantic owner's produced next shape.
func (v View) SepgsqlRelationPostAlter(relation *catalog.Relation) error {
	if v.Sepgsql.Policy == nil {
		return nil
	}
	if relation == nil {
		return ids.ErrCatalogReplaced
	}
	raw, err := v.Object(v.Ref(relation.Address()))
	if err != nil {
		return err
	}
	current, ok := raw.(*catalog.Relation)
	if !ok || current.RelationKind != relation.RelationKind {
		return ids.ErrCatalogReplaced
	}
	class := catalog.SepgsqlClass(relation, 0)
	if class == "" {
		return nil
	}
	if _, err = v.SepgsqlDecision(v.SepgsqlContext(relation.Address()), class, catalog.SepgsqlObjectKey(v.Catalog, relation, 0), sepgsql.Setattr, true); err != nil {
		return err
	}
	return v.observeSepgsql(relation.Address(), class, sepgsql.Setattr)
}
