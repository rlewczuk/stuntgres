package engine

func uniqueBacking(c *uniqueConstraint) *backingConstraint {
	return &backingConstraint{OID: c.OID, Name: c.Name, Index: c.Index, Kind: uniqueBackingKind, Primary: c.Primary}
}
func exclusionBacking(c *exclusionConstraint) *backingConstraint {
	return &backingConstraint{OID: c.OID, Name: c.Name, Index: c.Index, Kind: exclusionBackingKind}
}

func backingConstraintPresent(parent *relationDescriptor, index *relationDescriptor) bool {
	c := index.SQLIndex.Constraint
	if c == nil {
		return true
	}
	if c.Kind == uniqueBackingKind {
		for _, definition := range parent.UniqueConstraints {
			if definition.OID == c.OID && definition.Index == index.Address() && definition.Name == c.Name && definition.Primary == c.Primary {
				return true
			}
		}
	} else if c.Kind == exclusionBackingKind {
		for _, definition := range parent.ExclusionConstraints {
			if definition.OID == c.OID && definition.Index == index.Address() && definition.Name == c.Name && !c.Primary {
				return true
			}
		}
	}
	return false
}
