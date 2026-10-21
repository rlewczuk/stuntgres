package engine

import (
	"fmt"
	"maps"
	"slices"

	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/ids"
)

// Existing remapDefinitionOIDsLocked has already minted the actual final key
// group. Reissue only the exact captured relation/attribute and that group;
// neither this helper nor the certificate allocates a constraint or index.
func (e *Engine) remapPhysicalNotNull(next *catalog.State, previous map[ids.ObjectAddress]catalog.Descriptor) error {
	for address, descriptor := range next.Descriptors() {
		table, ok := descriptor.(*catalog.Relation)
		if !ok || len(table.Constraints.PhysicalNotNull) == 0 {
			continue
		}
		namespace, ok := next.Namespace(table.Namespace())
		if !ok {
			return fmt.Errorf("stuntgres: missing physical not-null namespace")
		}
		if namespace.Temporary != (ids.SessionToken{}) {
			continue
		}
		old, ok := previous[address].(*catalog.Relation)
		if !ok {
			return fmt.Errorf("stuntgres: missing physical not-null remap owner")
		}
		copy := *table
		copy.Constraints.PhysicalNotNull = maps.Clone(table.Constraints.PhysicalNotNull)
		supports := make(map[*catalog.PhysicalPrimarySupport]*catalog.PhysicalPrimarySupport)
		for attribute, proof := range table.Constraints.PhysicalNotNull {
			slot := table.IndexForAttribute(attribute)
			if proof.Cause == nil || proof.Cause.Self != proof.Cause || slot < 0 || old.Constraints.PhysicalNotNull[attribute].Cause != proof.Cause {
				return fmt.Errorf("stuntgres: changed physical not-null remap cause")
			}
			cause := *proof.Cause
			cause.Self = &cause
			cause.Ref = e.catalog.Domain.Ref(next.Epoch(), table.Address())
			proof.Cause = &cause
			proof.Usage = table.Columns[slot].Usage
			if support := proof.Support; support != nil {
				before, after := catalog.PhysicalPrimaryForAttribute(old, attribute), catalog.PhysicalPrimaryForAttribute(table, attribute)
				if before == nil || after == nil || before.OID != support.Constraint || before.Index != support.Index || after.Index != before.Index || !slices.Equal(before.Attributes, after.Attributes) {
					return fmt.Errorf("stuntgres: changed physical not-null remap primary")
				}
				replacement := supports[support]
				if replacement == nil {
					keys := make([]catalog.PhysicalPrimaryKeyUsage, len(after.Attributes))
					for i, key := range after.Attributes {
						keys[i] = catalog.PhysicalPrimaryKeyUsage{key, table.Columns[table.IndexForAttribute(key)].Usage}
					}
					replacement = &catalog.PhysicalPrimarySupport{Constraint: after.OID, Index: after.Index, Keys: keys}
					supports[support] = replacement
				}
				proof.Support = replacement
			}
			copy.Constraints.PhysicalNotNull[attribute] = proof
		}
		next.SetDescriptor(address, &copy)
		if err := ValidatePhysicalNotNull(e.catalog.read(next), &copy, PhysicalOrdinaryNotNull(&copy)); err != nil {
			return err
		}
	}
	return nil
}
