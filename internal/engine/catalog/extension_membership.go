package catalog

import (
	"fmt"
	"stuntgres/internal/engine/ids"
)

// WithExtensionMembership changes only the reciprocal membership projection.
// The command owner stages this detached copy with the extension's member list
// in the same candidate; installed provenance and effective ACL are unchanged.
func WithExtensionMembership(d Descriptor, extension ids.ObjectAddress, revision ids.DefinitionRevision) (Descriptor, error) {
	if extension != (ids.ObjectAddress{}) && (extension.ClassID != ids.ExtensionClassID || extension.ObjectID == 0 || extension.SubID != 0) {
		return nil, fmt.Errorf("stuntgres: invalid extension membership address")
	}
	switch d := d.(type) {
	case *Type:
		n := *d
		n.Extension = extension
		n.ObjectRevision = revision
		return &n, nil
	case *Procedure:
		n := *d
		n.Extension = extension
		n.ObjectRevision = revision
		return &n, nil
	case *Operator:
		n := *d
		n.Extension = extension
		n.ObjectRevision = revision
		return &n, nil
	case *OperatorClass:
		n := *d
		n.Extension = extension
		n.ObjectRevision = revision
		return &n, nil
	case *OperatorFamily:
		n := *d
		n.Extension = extension
		n.ObjectRevision = revision
		return &n, nil
	case *TSParser:
		n := *d
		n.Extension = extension
		n.ObjectRevision = revision
		return &n, nil
	case *TSTemplate:
		n := *d
		n.Extension = extension
		n.ObjectRevision = revision
		return &n, nil
	case *TSDictionary:
		n := *d
		n.Extension = extension
		n.ObjectRevision = revision
		return &n, nil
	case *TSConfiguration:
		n := *d
		n.Extension = extension
		n.ObjectRevision = revision
		return &n, nil
	case *Namespace:
		n := *d
		n.Extension = extension
		n.ObjectRevision = revision
		return &n, nil
	case *Relation:
		n := *d
		n.Extension = extension
		n.ObjectRevision = revision
		return &n, nil
	case *Collation:
		n := *d
		n.Extension = extension
		n.ObjectRevision = revision
		return &n, nil
	case *Conversion:
		n := *d
		n.Extension = extension
		n.ObjectRevision = revision
		return &n, nil
	case *Language:
		n := *d
		n.Extension = extension
		n.ObjectRevision = revision
		return &n, nil
	case *AccessMethod:
		n := *d
		n.Extension = extension
		n.ObjectRevision = revision
		return &n, nil
	case *EventTrigger:
		n := *d
		n.Extension = extension
		n.ObjectRevision = revision
		return &n, nil
	case *Cast:
		n := *d
		n.Extension = extension
		n.ObjectRevision = revision
		return &n, nil
	case *ForeignObject:
		if d.Address().ClassID == ids.ForeignWrapperClassID || d.Address().ClassID == ids.ForeignServerClassID {
			n := *d
			n.Extension = extension
			n.ObjectRevision = revision
			return &n, nil
		}
	}
	return nil, fmt.Errorf("stuntgres: descriptor is not an extension member class")
}

// ExtensionMemberClosure is the PostgreSQL direct-member recursion, not the
// broader ordinary dependency/removal closure. Constructors, indexes and owned
// sequences are not promoted to direct members merely by their dependencies.
func ExtensionMemberClosure(view Read, root Descriptor) ([]Descriptor, error) {
	var result []Descriptor
	seen := map[ids.ObjectAddress]bool{}
	var visit func(Descriptor) error
	visit = func(d Descriptor) error {
		if d == nil {
			return fmt.Errorf("stuntgres: missing extension member companion")
		}
		if seen[d.Address()] {
			return nil
		}
		seen[d.Address()] = true
		if !ExtensionMemberEligible(d) {
			return fmt.Errorf("stuntgres: descriptor is not an extension member class")
		}
		result = append(result, d)
		switch d := d.(type) {
		case *Type:
			if d.Array != 0 {
				if err := visit(view.Get(ids.ObjectAddress{ClassID: ids.TypeClassID, ObjectID: ids.OID(d.Array)})); err != nil {
					return err
				}
			}
			if d.SQLType.IsRange() {
				multi := d.SQLType.MultirangeType()
				if multi == nil {
					return fmt.Errorf("stuntgres: missing extension multirange companion")
				}
				if err := visit(view.Get(ids.ObjectAddress{ClassID: ids.TypeClassID, ObjectID: ids.OID(multi.ID())})); err != nil {
					return err
				}
			}
		case *Relation:
			if d.RowType != 0 {
				if err := visit(view.Get(ids.ObjectAddress{ClassID: ids.TypeClassID, ObjectID: ids.OID(d.RowType)})); err != nil {
					return err
				}
			}
		}
		return nil
	}
	if err := visit(root); err != nil {
		return nil, err
	}
	return result, nil
}
