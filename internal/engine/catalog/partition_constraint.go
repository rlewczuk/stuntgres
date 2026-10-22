package catalog

import "slices"

// CloneConstraintOrigin detaches the mutable provenance of a candidate from its
// captured descriptor. Published descriptors and their origin slices are immutable.
func CloneConstraintOrigin(source *InheritedConstraint) *InheritedConstraint {
	if source == nil {
		return nil
	}
	next := *source
	next.Origins = slices.Clone(source.Origins)
	next.Retired = slices.Clone(source.Retired)
	return &next
}

func ConstraintOrigin(source *InheritedConstraint) InheritedConstraint {
	if source == nil {
		return InheritedConstraint{Local: true}
	}
	return *source
}
