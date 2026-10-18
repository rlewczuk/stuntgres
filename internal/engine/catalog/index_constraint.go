package catalog

import (
	"stuntgres/internal/engine/ids"
)

// A backing index owns a common constraint address, not a UNIQUE alias. Key and
// timing semantics remain with their distinct immutable parent definitions.
type BackingConstraintKind uint8

const (
	UniqueBackingKind BackingConstraintKind = iota + 1
	ExclusionBackingKind
)

type BackingConstraint struct {
	OID     ids.OID
	Name    string
	Index   ids.ObjectAddress
	Kind    BackingConstraintKind
	Primary bool // UNIQUE-family backing identity; EXCLUDE is never primary.
}

func (d *IndexDefinition) IsExclusion() bool {
	return d.Constraint != nil && d.Constraint.Kind == ExclusionBackingKind
}
