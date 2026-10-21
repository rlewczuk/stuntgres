package engine

import (
	"fmt"
	"maps"
	"slices"
	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/index"
	"stuntgres/internal/engine/table"
)

// Rebind only after the existing ordinary TYPE/unique/index providers finish.
// This does not mint constraints, indexes or NN records and performs no row work.
func rebindPhysicalNotNullType(stage *CatalogStage, parent *catalog.Relation, changes []index.ColumnTransformation) (*catalog.Relation, error) {
	if len(parent.Constraints.PhysicalNotNull) == 0 {
		return parent, nil
	}
	next := *parent
	table.PrunePhysicalNotNull(&next)
	var support *catalog.PhysicalPrimarySupport
	copied := next.Constraints.PhysicalNotNull != nil && len(next.Constraints.PhysicalNotNull) != len(parent.Constraints.PhysicalNotNull)
	for attribute, proof := range next.Constraints.PhysicalNotNull {
		primary := catalog.PhysicalPrimaryForAttribute(&next, attribute)
		if primary == nil && (proof.Support != nil || proof.Retired == nil && proof.Cause.Kind != catalog.PhysicalNotNullImportFlag) {
			return nil, fmt.Errorf("stuntgres: missing rebuilt physical not-null primary")
		}
		// Included payload TYPE changes can rebuild the real primary group
		// without changing a key's Usage. Follow that actual group replacement.
		touches := primary != nil && proof.Support != nil && (primary.OID != proof.Support.Constraint || primary.Index != proof.Support.Index)
		for _, change := range changes {
			if primary != nil && slices.Contains(primary.Attributes, change.Attribute) || primary == nil && change.Attribute == attribute {
				touches = true
				break
			}
		}
		if !touches {
			continue
		}
		if !copied {
			next.Constraints.PhysicalNotNull = maps.Clone(next.Constraints.PhysicalNotNull)
			copied = true
		}
		if primary != nil && support == nil {
			// The primary is the real rebuilt group. Do not reuse its old key
			// Usage merely because a stale proof still names the same position.
			keys := make([]catalog.PhysicalPrimaryKeyUsage, len(primary.Attributes))
			for i, key := range primary.Attributes {
				keys[i] = catalog.PhysicalPrimaryKeyUsage{key, next.Columns[next.IndexForAttribute(key)].Usage}
			}
			support = &catalog.PhysicalPrimarySupport{Constraint: primary.OID, Index: primary.Index, Keys: keys}
		}
		proof.Usage = next.Columns[next.IndexForAttribute(attribute)].Usage
		if primary != nil {
			proof.Support = support
		}
		next.Constraints.PhysicalNotNull[attribute] = proof
	}
	if !copied && len(next.Constraints.PhysicalNotNull) == len(parent.Constraints.PhysicalNotNull) {
		return parent, nil
	}
	if err := stage.Replace(&next); err != nil {
		return nil, err
	}
	return &next, nil
}
