package engine

import (
	"fmt"
	"maps"
	"slices"

	"stuntgres/internal/engine/ids"
)

// Map actual live relation/index addresses with the existing replacement map.
// Archived removed identities retain their genuine source lifetime namespace.
func remapPhysicalNotNullReferences(old, next *relationDescriptor, mapping map[ids.ObjectAddress]ids.ObjectAddress) error {
	if len(old.PhysicalNotNull) == 0 {
		return nil
	}
	next.PhysicalNotNull = maps.Clone(old.PhysicalNotNull)
	supports := make(map[*physicalPrimarySupport]*physicalPrimarySupport)
	for attribute, proof := range old.PhysicalNotNull {
		slot := old.indexForAttribute(attribute)
		if proof.Cause == nil || proof.Cause.Self != proof.Cause || slot < 0 || proof.Usage != old.Columns[slot].Usage {
			return fmt.Errorf("stuntgres: invalid physical not-null remap source")
		}
		cause := *proof.Cause
		cause.Self = &cause
		cause.Ref.Address = next.Address()
		proof.Cause = &cause
		if support := proof.Support; support != nil {
			primary := physicalPrimaryForAttribute(old, attribute)
			if primary == nil || support.Constraint != primary.OID || support.Index != primary.Index || len(support.Keys) != len(primary.Attributes) {
				return fmt.Errorf("stuntgres: missing physical not-null source primary")
			}
			for i, key := range support.Keys {
				if key.Attribute != primary.Attributes[i] || key.Usage != old.Columns[old.indexForAttribute(key.Attribute)].Usage {
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
		next.PhysicalNotNull[attribute] = proof
	}
	return nil
}

// Existing remapDefinitionOIDsLocked has already minted the actual final key
// group. Reissue only the exact captured relation/attribute and that group;
// neither this helper nor the certificate allocates a constraint or index.
func (e *Engine) remapPhysicalNotNull(next *catalogState, previous map[ids.ObjectAddress]Descriptor) error {
	for address, descriptor := range next.Descriptors() {
		table, ok := descriptor.(*relationDescriptor)
		if !ok || len(table.PhysicalNotNull) == 0 {
			continue
		}
		namespace, ok := next.Namespace(table.Namespace())
		if !ok {
			return fmt.Errorf("stuntgres: missing physical not-null namespace")
		}
		if namespace.Temporary != (ids.SessionToken{}) {
			continue
		}
		old, ok := previous[address].(*relationDescriptor)
		if !ok {
			return fmt.Errorf("stuntgres: missing physical not-null remap owner")
		}
		copy := *table
		copy.PhysicalNotNull = maps.Clone(table.PhysicalNotNull)
		supports := make(map[*physicalPrimarySupport]*physicalPrimarySupport)
		for attribute, proof := range table.PhysicalNotNull {
			slot := table.indexForAttribute(attribute)
			if proof.Cause == nil || proof.Cause.Self != proof.Cause || slot < 0 || old.PhysicalNotNull[attribute].Cause != proof.Cause {
				return fmt.Errorf("stuntgres: changed physical not-null remap cause")
			}
			cause := *proof.Cause
			cause.Self = &cause
			cause.Ref = e.catalog.Domain.Ref(next.epoch, table.Address())
			proof.Cause = &cause
			proof.Usage = table.Columns[slot].Usage
			if support := proof.Support; support != nil {
				before, after := physicalPrimaryForAttribute(old, attribute), physicalPrimaryForAttribute(table, attribute)
				if before == nil || after == nil || before.OID != support.Constraint || before.Index != support.Index || after.Index != before.Index || !slices.Equal(before.Attributes, after.Attributes) {
					return fmt.Errorf("stuntgres: changed physical not-null remap primary")
				}
				replacement := supports[support]
				if replacement == nil {
					keys := make([]physicalPrimaryKeyUsage, len(after.Attributes))
					for i, key := range after.Attributes {
						keys[i] = physicalPrimaryKeyUsage{key, table.Columns[table.indexForAttribute(key)].Usage}
					}
					replacement = &physicalPrimarySupport{Constraint: after.OID, Index: after.Index, Keys: keys}
					supports[support] = replacement
				}
				proof.Support = replacement
			}
			copy.PhysicalNotNull[attribute] = proof
		}
		next.setDescriptor(address, &copy)
		if err := validatePhysicalNotNull(e.catalog.read(next), &copy, physicalOrdinaryNotNull(&copy)); err != nil {
			return err
		}
	}
	return nil
}

func physicalOrdinaryNotNull(table *relationDescriptor) map[int32]bool {
	ordinary := make(map[int32]bool, len(table.NotNullConstraints))
	for _, fact := range table.NotNullConstraints {
		ordinary[fact.Attribute] = true
	}
	return ordinary
}
