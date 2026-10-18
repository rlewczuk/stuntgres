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
		if proof.cause == nil || proof.cause.self != proof.cause || slot < 0 || proof.usage != old.Columns[slot].Usage {
			return fmt.Errorf("stuntgres: invalid physical not-null remap source")
		}
		cause := *proof.cause
		cause.self = &cause
		cause.ref.Address = next.address
		proof.cause = &cause
		if support := proof.support; support != nil {
			primary := physicalPrimaryForAttribute(old, attribute)
			if primary == nil || support.constraint != primary.OID || support.index != primary.Index || len(support.keys) != len(primary.Attributes) {
				return fmt.Errorf("stuntgres: missing physical not-null source primary")
			}
			for i, key := range support.keys {
				if key.attribute != primary.Attributes[i] || key.usage != old.Columns[old.indexForAttribute(key.attribute)].Usage {
					return fmt.Errorf("stuntgres: invalid physical not-null source key usage")
				}
			}
			replacement := supports[support]
			if replacement == nil {
				copy := *support
				if address, moved := mapping[copy.index]; moved {
					copy.index = address
				}
				replacement = &copy
				supports[support] = replacement
			}
			proof.support = replacement
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
		namespace, ok := next.Namespace(table.namespace)
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
			if proof.cause == nil || proof.cause.self != proof.cause || slot < 0 || old.PhysicalNotNull[attribute].cause != proof.cause {
				return fmt.Errorf("stuntgres: changed physical not-null remap cause")
			}
			cause := *proof.cause
			cause.self = &cause
			cause.ref = e.catalog.domain.Ref(next.epoch, table.address)
			proof.cause = &cause
			proof.usage = table.Columns[slot].Usage
			if support := proof.support; support != nil {
				before, after := physicalPrimaryForAttribute(old, attribute), physicalPrimaryForAttribute(table, attribute)
				if before == nil || after == nil || before.OID != support.constraint || before.Index != support.index || after.Index != before.Index || !slices.Equal(before.Attributes, after.Attributes) {
					return fmt.Errorf("stuntgres: changed physical not-null remap primary")
				}
				replacement := supports[support]
				if replacement == nil {
					keys := make([]physicalPrimaryKeyUsage, len(after.Attributes))
					for i, key := range after.Attributes {
						keys[i] = physicalPrimaryKeyUsage{key, table.Columns[table.indexForAttribute(key)].Usage}
					}
					replacement = &physicalPrimarySupport{constraint: after.OID, index: after.Index, keys: keys}
					supports[support] = replacement
				}
				proof.support = replacement
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
