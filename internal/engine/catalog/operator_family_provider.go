package catalog

import (
	"fmt"
	"stuntgres/internal/engine/ids"
)

type OperatorFamilyProvider struct{ Class ids.OID }

func (p OperatorFamilyProvider) ClassID() ids.OID { return p.Class }
func (p OperatorFamilyProvider) Validate(v Read, d Descriptor) error {
	if d == nil || d.Address().ClassID != p.Class || d.Address().ObjectID == 0 || d.Address().SubID != 0 || d.Revision() == 0 || v.NameAddress(DescriptorNameKey(d)) != d.Address() {
		return fmt.Errorf("stuntgres: invalid operator family descriptor authority")
	}
	if err := ValidateExtensionMembership(v, d); err != nil {
		return err
	}
	if owner := OperatorFamilyMemberOwner(d); owner != (ids.ObjectAddress{}) {
		if owner.SubID != 0 {
			return fmt.Errorf("stuntgres: invalid internal member owner")
		}
		var family ids.OID
		switch member := d.(type) {
		case *OperatorFamilyOperator:
			family = member.Family
		case *OperatorFamilySupport:
			family = member.Family
		}
		switch owner.ClassID {
		case ids.OperatorClassCatalogID:
			class, ok := v.OperatorClass(owner.ObjectID)
			if !ok || class.Family != family {
				return fmt.Errorf("stuntgres: invalid internal class owner")
			}
		case ids.OperatorFamilyClassID:
			if _, ok := v.OperatorFamily(owner); !ok || owner.ObjectID != family {
				return fmt.Errorf("stuntgres: invalid internal family owner")
			}
		default:
			return fmt.Errorf("stuntgres: invalid internal member owner")
		}
	}
	switch d := d.(type) {
	case *OperatorFamily:
		if err := ValidateInstalledClassOrigin(v, d); err != nil {
			return err
		}
		method, exists := v.AccessMethod(d.Method)
		if _, ok := v.Namespace(d.Namespace()); !ok || d.Name() == "" || !exists || method.MethodKind() != "i" {
			return fmt.Errorf("stuntgres: invalid operator family")
		}
	case *OperatorFamilyOperator:
		f, ok := v.OperatorFamily(ids.ObjectAddress{ClassID: ids.OperatorFamilyClassID, ObjectID: d.Family})
		if !ok || f.Method != d.Method || d.Left == 0 || d.Right == 0 || d.Operator == 0 || d.Number <= 0 || d.Purpose != 's' && d.Purpose != 'o' || d.Purpose == 's' && d.SortFamily != 0 {
			return fmt.Errorf("stuntgres: invalid operator family operator")
		}
		method, exists := v.AccessMethod(f.Method)
		if !exists || p.Class != ids.AccessMethodOperatorClassID || (method.Native == BtreeAccessMethod || method.Native == BrinAccessMethod) && d.Number > 5 || method.Native == HashAccessMethod && d.Number > 1 || d.Purpose == 'o' && (method.Native != GistAccessMethod && method.Native != SpgistAccessMethod || d.SortFamily == 0) {
			return fmt.Errorf("stuntgres: invalid family operator method")
		}
		if original, seeded := builtinFamilyOperatorsByOID[d.Address().ObjectID]; seeded {
			if *original != *d {
				return fmt.Errorf("stuntgres: altered original family operator")
			}
		} else {
			operator, ok := v.Operator(ids.ObjectAddress{ClassID: ids.OperatorClassID, ObjectID: d.Operator})
			if !ok || operator.Left != d.Left || operator.Right != d.Right || d.Purpose == 's' && operator.Result != 16 {
				return fmt.Errorf("stuntgres: invalid family operator reference")
			}
			for _, other := range v.Descriptors() {
				if member, ok := other.(*OperatorFamilyOperator); ok && member.Address() != d.Address() && member.Family == d.Family && member.Operator == d.Operator && member.Purpose == d.Purpose {
					return fmt.Errorf("stuntgres: duplicate family operator identity")
				}
			}
		}
	case *OperatorFamilySupport:
		family, ok := v.OperatorFamily(ids.ObjectAddress{ClassID: ids.OperatorFamilyClassID, ObjectID: d.Family})
		if !ok || p.Class != ids.AccessMethodProcedureClassID || d.Left == 0 || d.Right == 0 || d.Function == 0 || d.Number <= 0 {
			return fmt.Errorf("stuntgres: invalid operator family support")
		}
		method, exists := v.AccessMethod(family.Method)
		if !exists {
			return fmt.Errorf("stuntgres: invalid family method")
		}
		maximum := int16(12)
		if method.Native == BtreeAccessMethod {
			maximum = 6
		} else if method.Native == HashAccessMethod {
			maximum = 3
		} else if method.Native == BrinAccessMethod {
			maximum = 15
		} else if method.Native == SpgistAccessMethod {
			maximum = 7
		}
		if d.Number > maximum {
			return fmt.Errorf("stuntgres: invalid family support number")
		}
		if original, seeded := builtinFamilySupportsByOID[d.Address().ObjectID]; seeded {
			if *original != *d {
				return fmt.Errorf("stuntgres: altered original family support")
			}
		} else if _, ok := v.Procedure(ids.ObjectAddress{ClassID: ids.ProcedureClassID, ObjectID: d.Function}); !ok {
			if _, ok := BuiltinRoutineResultByOID(d.Function); !ok {
				return fmt.Errorf("stuntgres: invalid family support routine reference")
			}
		}
	default:
		return fmt.Errorf("stuntgres: invalid operator family descriptor kind")
	}
	return nil
}

// OperatorFamilySeeded identifies original initdb objects, not loose user
// additions to an initdb family. Original optional/cross-type rows are pinned.
func OperatorFamilySeeded(d Descriptor) bool {
	switch d := d.(type) {
	case *OperatorFamily:
		_, ok := builtinFamilyNamesByOID[d.Address().ObjectID]
		return ok
	case *OperatorFamilyOperator:
		original, ok := builtinFamilyOperatorsByOID[d.Address().ObjectID]
		return ok && original.Address() == d.Address()
	case *OperatorFamilySupport:
		original, ok := builtinFamilySupportsByOID[d.Address().ObjectID]
		return ok && original.Address() == d.Address()
	}
	return false
}

// OperatorFamilySeedName is the canonical release spelling used only to
// reconstruct admitted seed metadata changes, never to select runtime members.
func OperatorFamilySeedName(id ids.OID) (string, bool) {
	name, ok := builtinFamilyNamesByOID[id]
	return name, ok
}
func OperatorFamilySeedModified(d *OperatorFamily) bool {
	name, seeded := OperatorFamilySeedName(d.Address().ObjectID)
	return seeded && (d.Name() != name || d.Namespace().ObjectID != ids.PGCatalogNamespaceOID || d.Owner.ID() != 10)
}
