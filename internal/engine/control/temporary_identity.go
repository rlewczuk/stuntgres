package control

import (
	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/ids"
)

// TemporaryCollisionMapping gives every imported identity of the candidate
// next that collides with a retained temporary object a fresh OID, and
// returns the old-to-new address mapping. Aggregate helper identities are
// mapped too. Retained TEMP definitions cannot be reminted to make room for
// imports. Large-object OIDs are class-local and may equal retained relation
// OIDs. allocate is the engine's OID allocator; the caller holds Engine.mu
// for the whole call.
func TemporaryCollisionMapping(next *catalog.State, temporary map[ids.ObjectAddress]catalog.Descriptor, allocate func() (ids.OID, error)) (map[ids.ObjectAddress]ids.ObjectAddress, error) {
	retainedIDs := make(map[ids.OID]bool)
	for address, descriptor := range temporary {
		retainedIDs[address.ObjectID] = true
		catalog.ReserveDefinitionOIDs(retainedIDs, descriptor)
	}
	reservedIDs := make(map[ids.OID]bool, next.DescriptorCount()+len(retainedIDs))
	for id := range retainedIDs {
		reservedIDs[id] = true
	}
	for address, descriptor := range next.Descriptors() {
		reservedIDs[address.ObjectID] = true
		catalog.ReserveDefinitionOIDs(reservedIDs, descriptor)
	}
	mapping := make(map[ids.ObjectAddress]ids.ObjectAddress)
	remapCollision := func(address ids.ObjectAddress) error {
		if address.ClassID == ids.LargeObjectClassID || !retainedIDs[address.ObjectID] {
			return nil
		}
		for {
			oid, err := allocate()
			if err != nil {
				return err
			}
			if reservedIDs[oid] {
				continue
			}
			destination := address
			destination.ObjectID = oid
			mapping[address] = destination
			reservedIDs[oid] = true
			return nil
		}
	}
	for address, descriptor := range next.Descriptors() {
		if err := remapCollision(address); err != nil {
			return nil, err
		}
		if proc, ok := descriptor.(*catalog.Procedure); ok && proc.Aggregate != nil && proc.Implementation != nil {
			// These are mapping-only identities, not callable descriptors.
			// Publish their mapping before scalar/default/expression remapping.
			for _, helper := range proc.Aggregate.Helpers {
				if err := remapCollision(ids.ObjectAddress{ClassID: ids.ProcedureClassID, ObjectID: helper.ID}); err != nil {
					return nil, err
				}
			}
		}
	}
	return mapping, nil
}
