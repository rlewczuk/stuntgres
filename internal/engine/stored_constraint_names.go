package engine

import (
	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/ids"
)

// Attached constraints have catalog identities but no standalone descriptors.
// Resolve them from their canonical parent metadata, never a spelling registry.
func constraintName(parent *catalog.Relation, address ids.ObjectAddress) string {
	if parent == nil || address.ClassID != 2606 || address.ObjectID == 0 || address.SubID != 0 {
		return ""
	}
	for _, c := range parent.Constraints.Unique {
		if c.OID == address.ObjectID {
			return c.Name
		}
	}
	for _, c := range parent.Constraints.Exclusion {
		if c.OID == address.ObjectID {
			return c.Name
		}
	}
	for _, c := range parent.Constraints.Check {
		if c.OID == address.ObjectID {
			return c.Name
		}
	}
	for _, c := range parent.Constraints.NotNull {
		if c.OID == address.ObjectID {
			return c.Name
		}
	}
	for _, c := range parent.Constraints.ForeignKeys {
		if c.OID == address.ObjectID {
			return c.Name
		}
	}
	return ""
}

func constraintParent(view catalog.Read, address ids.ObjectAddress) *catalog.Relation {
	if address.ClassID != 2606 || address.ObjectID == 0 || address.SubID != 0 {
		return nil
	}
	for _, descriptor := range view.Descriptors() {
		if parent, ok := descriptor.(*catalog.Relation); ok && constraintName(parent, address) != "" {
			return parent
		}
	}
	return nil
}
