package engine

import "stuntgres/internal/engine/ids"

// A backing index owns a common constraint address, not a UNIQUE alias. Key and
// timing semantics remain with their distinct immutable parent definitions.
type backingConstraintKind uint8

const (
	uniqueBackingKind backingConstraintKind = iota + 1
	exclusionBackingKind
)

type backingConstraint struct {
	OID     ids.OID
	Name    string
	Index   ids.ObjectAddress
	Kind    backingConstraintKind
	Primary bool // UNIQUE-family backing identity; EXCLUDE is never primary.
}

func uniqueBacking(c *uniqueConstraint) *backingConstraint {
	return &backingConstraint{OID: c.OID, Name: c.Name, Index: c.Index, Kind: uniqueBackingKind, Primary: c.Primary}
}
func exclusionBacking(c *exclusionConstraint) *backingConstraint {
	return &backingConstraint{OID: c.OID, Name: c.Name, Index: c.Index, Kind: exclusionBackingKind}
}
func (d *indexDefinition) IsExclusion() bool {
	return d.Constraint != nil && d.Constraint.Kind == exclusionBackingKind
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
