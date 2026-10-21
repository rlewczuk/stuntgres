package catalog

import (
	"slices"
)

func PhysicalPrimaryForAttribute(table *Relation, attribute int32) *UniqueConstraint {
	for i := range table.Constraints.Unique {
		constraint := &table.Constraints.Unique[i]
		if constraint.Primary && slices.Contains(constraint.Attributes, attribute) {
			return constraint
		}
	}
	return nil
}

func CapturePhysicalPrimarySupport(table *Relation, primary *UniqueConstraint) *PhysicalPrimarySupport {
	for _, proof := range table.Constraints.PhysicalNotNull {
		if proof.Support != nil && proof.Support.Constraint == primary.OID && proof.Support.Index == primary.Index {
			return proof.Support
		}
	}
	keys := make([]PhysicalPrimaryKeyUsage, len(primary.Attributes))
	for i, attribute := range primary.Attributes {
		keys[i] = PhysicalPrimaryKeyUsage{attribute, table.Columns[table.IndexForAttribute(attribute)].Usage}
	}
	return &PhysicalPrimarySupport{Constraint: primary.OID, Index: primary.Index, Keys: keys}
}
