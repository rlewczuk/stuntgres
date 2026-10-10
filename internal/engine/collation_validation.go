package engine

import (
	"fmt"
	"stuntgres/internal/sqltype"
)

// An immutable execution capability is not proof that its catalog identity is
// still live. Stored plans must retain both authorities through publication.
func validateUsageCollation(view CatalogRead, u sqltype.Usage) error {
	if u.Collation.ID == 0 {
		return nil
	}
	d, ok := view.state.descriptors[ObjectAddress{ClassID: CollationClassID, ObjectID: OID(u.Collation.ID)}].(*collationDescriptor)
	if !ok {
		return fmt.Errorf("stuntgres: missing stored collation")
	}
	ref, err := d.usage()
	if err != nil || ref != u.Collation {
		return fmt.Errorf("stuntgres: invalid stored collation authority")
	}
	return nil
}

// Captured provider manifests may outlive a descriptor revision, but never
// authorize a missing identity, an attribute address, or an unavailable provider.
func validateCollationProvider(view CatalogRead, ref ObjectRef) error {
	if ref.Address.SubID != 0 {
		return fmt.Errorf("stuntgres: invalid stored collation provider")
	}
	d, ok := view.state.descriptors[ref.Address].(*collationDescriptor)
	if !ok {
		return fmt.Errorf("stuntgres: invalid stored collation provider")
	}
	if capability, err := d.usage(); err != nil || capability.ID != uint32(ref.Address.ObjectID) {
		return fmt.Errorf("stuntgres: invalid stored collation provider")
	}
	return nil
}
