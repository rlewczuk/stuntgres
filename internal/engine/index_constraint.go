package engine

import "stuntgres/internal/engine/catalog"

func UniqueBacking(c *catalog.UniqueConstraint) *catalog.BackingConstraint {
	return &catalog.BackingConstraint{OID: c.OID, Name: c.Name, Index: c.Index, Kind: catalog.UniqueBackingKind, Primary: c.Primary}
}
func ExclusionBacking(c *catalog.ExclusionConstraint) *catalog.BackingConstraint {
	return &catalog.BackingConstraint{OID: c.OID, Name: c.Name, Index: c.Index, Kind: catalog.ExclusionBackingKind}
}

func BackingConstraintPresent(parent *catalog.Relation, index *catalog.Relation) bool {
	c := index.SQLIndex.Constraint
	if c == nil {
		return true
	}
	if c.Kind == catalog.UniqueBackingKind {
		for _, definition := range parent.Constraints.Unique {
			if definition.OID == c.OID && definition.Index == index.Address() && definition.Name == c.Name && definition.Primary == c.Primary {
				return true
			}
		}
	} else if c.Kind == catalog.ExclusionBackingKind {
		for _, definition := range parent.Constraints.Exclusion {
			if definition.OID == c.OID && definition.Index == index.Address() && definition.Name == c.Name && !c.Primary {
				return true
			}
		}
	}
	return false
}
