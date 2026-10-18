package engine

import (
	"fmt"
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/sqltype"
)

// An immutable execution capability is not proof that its catalog identity is
// still live. Stored plans must retain both authorities through publication.
func validateUsageCollation(view CatalogRead, u sqltype.Usage) error {
	return validateUsageCollationAt(view, nil, u)
}

func validateUsageCollationAt(view CatalogRead, reader definitionReader, u sqltype.Usage) error {
	if u.Collation.ID == 0 {
		return nil
	}
	descriptor, _ := definitionDescriptorAt(view, reader, ids.ObjectAddress{ClassID: ids.CollationClassID, ObjectID: ids.OID(u.Collation.ID)})
	d, ok := descriptor.(*collationDescriptor)
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
func validateCollationProvider(view CatalogRead, ref ids.ObjectRef) error {
	if ref.Address.SubID != 0 {
		return fmt.Errorf("stuntgres: invalid stored collation provider")
	}
	d, ok := view.Collation(ref.Address)
	if !ok {
		return fmt.Errorf("stuntgres: invalid stored collation provider")
	}
	if capability, err := d.usage(); err != nil || capability.ID != uint32(ref.Address.ObjectID) {
		return fmt.Errorf("stuntgres: invalid stored collation provider")
	}
	return nil
}

// Catalog-valued REGCOLLATION constants retain identity, not a comparison
// capability. Other expressions and any actual use of this collation keep the
// existing provider validation, even if the same OID also appears as an alias.
func validateStoredCollationDependency(view CatalogRead, ref ids.ObjectRef, node BoundExpr) error {
	err := validateCollationProvider(view, ref)
	if err == nil {
		return nil
	}
	if ref.Address.ClassID != ids.CollationClassID || ref.Address.SubID != 0 {
		return err
	}
	if _, exists := view.Collation(ref.Address); !exists {
		return err
	}
	identity, comparison := false, false
	walkBoundExpression(node, func(child BoundExpr) {
		usage := child.ResultUsage()
		if ids.OID(usage.Collation.ID) == ref.Address.ObjectID {
			comparison = true
		}
		literal, ok := child.(literalExpression)
		if !ok || literal.value.IsNull() || literal.value.Type() == nil || literal.value.Type().ID() != sqltype.Regcollation || usage.Type == nil || usage.Type.ID() != sqltype.Regcollation {
			return
		}
		if id, failure := literal.value.ObjectOID(); failure == nil && ids.OID(id) == ref.Address.ObjectID {
			identity = true
		}
	})
	if identity && !comparison {
		return nil
	}
	return err
}
