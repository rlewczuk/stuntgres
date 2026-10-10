package engine

// A backing index owns a common constraint address, not a UNIQUE alias. Key and
// timing semantics remain with their distinct immutable parent definitions.
type backingConstraintKind uint8

const (
	uniqueBackingKind backingConstraintKind = iota + 1
	exclusionBackingKind
)

type backingConstraint struct {
	OID     OID
	Name    string
	Index   ObjectAddress
	kind    backingConstraintKind
	Primary bool // UNIQUE-family backing identity; EXCLUDE is never primary.
}

func uniqueBacking(c *uniqueConstraint) *backingConstraint {
	return &backingConstraint{OID: c.OID, Name: c.Name, Index: c.Index, kind: uniqueBackingKind, Primary: c.Primary}
}
func exclusionBacking(c *exclusionConstraint) *backingConstraint {
	return &backingConstraint{OID: c.OID, Name: c.Name, Index: c.Index, kind: exclusionBackingKind}
}
func (d *indexDefinition) isExclusion() bool {
	return d.constraint != nil && d.constraint.kind == exclusionBackingKind
}
func backingConstraintPresent(parent *relationDescriptor, index *relationDescriptor) bool {
	c := index.sqlIndex.constraint
	if c == nil {
		return true
	}
	if c.kind == uniqueBackingKind {
		for _, definition := range parent.uniqueConstraints {
			if definition.OID == c.OID && definition.Index == index.address && definition.Name == c.Name && definition.Primary == c.Primary {
				return true
			}
		}
	} else if c.kind == exclusionBackingKind {
		for _, definition := range parent.exclusionConstraints {
			if definition.OID == c.OID && definition.Index == index.address && definition.Name == c.Name && !c.Primary {
				return true
			}
		}
	}
	return false
}
