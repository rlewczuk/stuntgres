package engine

import (
	"fmt"
	"maps"

	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/ids"
)

// Map actual live relation/index addresses with the existing replacement map.
// Archived removed identities retain their genuine source lifetime namespace.
func remapPhysicalNotNullReferences(old, next *catalog.Relation, mapping map[ids.ObjectAddress]ids.ObjectAddress) error {
	if len(old.Constraints.PhysicalNotNull) == 0 {
		return nil
	}
	next.Constraints.PhysicalNotNull = maps.Clone(old.Constraints.PhysicalNotNull)
	supports := make(map[*catalog.PhysicalPrimarySupport]*catalog.PhysicalPrimarySupport)
	for attribute, proof := range old.Constraints.PhysicalNotNull {
		slot := old.IndexForAttribute(attribute)
		if proof.Cause == nil || proof.Cause.Self != proof.Cause || slot < 0 || proof.Usage != old.Columns[slot].Usage {
			return fmt.Errorf("stuntgres: invalid physical not-null remap source")
		}
		cause := *proof.Cause
		cause.Self = &cause
		cause.Ref.Address = next.Address()
		proof.Cause = &cause
		if support := proof.Support; support != nil {
			primary := catalog.PhysicalPrimaryForAttribute(old, attribute)
			if primary == nil || support.Constraint != primary.OID || support.Index != primary.Index || len(support.Keys) != len(primary.Attributes) {
				return fmt.Errorf("stuntgres: missing physical not-null source primary")
			}
			for i, key := range support.Keys {
				if key.Attribute != primary.Attributes[i] || key.Usage != old.Columns[old.IndexForAttribute(key.Attribute)].Usage {
					return fmt.Errorf("stuntgres: invalid physical not-null source key usage")
				}
			}
			replacement := supports[support]
			if replacement == nil {
				copy := *support
				if address, moved := mapping[copy.Index]; moved {
					copy.Index = address
				}
				replacement = &copy
				supports[support] = replacement
			}
			proof.Support = replacement
		}
		next.Constraints.PhysicalNotNull[attribute] = proof
	}
	return nil
}

func physicalOrdinaryNotNull(table *catalog.Relation) map[int32]bool {
	ordinary := make(map[int32]bool, len(table.Constraints.NotNull))
	for _, fact := range table.Constraints.NotNull {
		ordinary[fact.Attribute] = true
	}
	return ordinary
}
