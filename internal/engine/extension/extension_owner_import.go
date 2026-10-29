package extension

import (
	"context"
	"fmt"
	"slices"

	"stuntgres/internal/engine/access"
	"stuntgres/internal/engine/authorization"
	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/ids"
)

// ImportOwners validates every record against real reconstructed declarations
// before changing even the private candidate. No label manufactures provenance.
func ImportOwners(ctx context.Context, view catalog.Read, roles authorization.Read, records []OwnerRecord) (bool, error) {
	return importOwners(ctx, view, roles, records, false)
}

// ValidateOwnerReplay checks installed declaration authority at its first SQL
// OWNER use, after installation identities are final but before ordinary objects
// and final symbolic membership exist. It shares the complete import validator;
// it never changes even the private candidate. ImportOwners must still run after
// all statements to validate ordinary targets, current membership and coverage.
func ValidateOwnerReplay(ctx context.Context, view catalog.Read, roles authorization.Read, records []OwnerRecord) error {
	_, err := importOwners(ctx, view, roles, records, true)
	return err
}

func importOwners(ctx context.Context, view catalog.Read, roles authorization.Read, records []OwnerRecord, replay bool) (bool, error) {
	if len(records) == 0 {
		return false, nil
	}
	if view.State() == nil || view.Owner() == nil || !roles.BelongsTo(view.Owner().Principals) {
		return false, fmt.Errorf("stuntgres: invalid extension owner import authority")
	}
	invalid := func() (bool, error) { return false, fmt.Errorf("stuntgres: invalid extension owner metadata") }
	identities := map[string]catalog.Descriptor{}
	for _, d := range view.Descriptors() {
		m, err := OwnerIdentity(view, d)
		if err == nil {
			key := ownerIdentityKey(m)
			if identities[key] != nil {
				return invalid()
			}
			identities[key] = d
		}
	}
	replacements := map[ids.ObjectAddress]catalog.Descriptor{}
	seen := map[ids.ObjectAddress]bool{}
	cohorts := map[ids.ObjectAddress]bool{}
	for _, record := range records {
		if err := ctx.Err(); err != nil {
			return false, err
		}
		implementation, ok := view.Owner().Extensions[record.Name]
		if !ok || implementation.Versions().InstallPath(record.Version) == nil {
			return invalid()
		}
		var ext *catalog.Extension
		if record.Present {
			ext, ok = view.Extension(view.NameAddress(ids.NameKey{ClassID: ids.ExtensionClassID, Name: record.Name}))
			if !ok || ext.Version != record.Version {
				return invalid()
			}
			ns, ok := view.Namespace(ext.TargetNamespace)
			if !ok || ns.Name() != record.Schema {
				return invalid()
			}
			owner, ok := roles.Lookup(record.Owner)
			if !ok {
				return invalid()
			}
			if ext.Owner != owner.Ref {
				n := *ext
				n.Owner = owner.Ref
				replacements[ext.Address()] = &n
			}
		} else if record.Owner != "" || record.Schema != "" {
			return invalid()
		}
		var cohort ids.ObjectAddress
		declarations := map[string]bool{}
		ordinary := map[ids.ObjectAddress]bool{}
		for _, member := range record.Members {
			if !validOwnerIdentity(member) {
				return invalid()
			}
			if replay && member.Declaration == nil {
				continue // Ordinary SQL creators have not necessarily run yet.
			}
			d := identities[ownerIdentityKey(member)]
			if d == nil || seen[d.Address()] {
				return invalid()
			}
			seen[d.Address()] = true
			membership := catalog.ExtensionMembership(d)
			if !replay && member.Extension == "" {
				if membership != (ids.ObjectAddress{}) {
					return invalid()
				}
			} else if !replay {
				current, ok := view.Extension(membership)
				if !ok || current.Name() != member.Extension {
					return invalid()
				}
			}
			origin := catalog.DescriptorOrigin(d)
			if member.Declaration == nil {
				if origin != nil || ext == nil || membership != ext.Address() || member.Version != "" || member.Owner != "" || member.ACL != nil {
					return invalid()
				}
				ordinary[d.Address()] = true
				continue
			}
			declarationKey := fmt.Sprint(member.Version, ":", *member.Declaration)
			if !origin.Valid(d.Address()) || origin.Installation.Provider != record.Name || origin.Installation.Version != member.Version || origin.Installation.Implementation != implementation || origin.Declaration != *member.Declaration || declarations[declarationKey] {
				return invalid()
			}
			declarations[declarationKey] = true
			if cohort == (ids.ObjectAddress{}) {
				cohort = origin.Installation.Cohort
			} else if cohort != origin.Installation.Cohort {
				return invalid()
			}
			if err := validateInstalledDeclaration(view, d); err != nil {
				return false, err
			}
			principal, oldACL, _ := access.DescriptorACL(d)
			if principal == (authorization.PrincipalRef{}) {
				if member.Owner != "" || member.ACL != nil {
					return invalid()
				}
				continue
			}
			owner, ok := roles.Lookup(member.Owner)
			if !ok {
				return false, fmt.Errorf("stuntgres: extension member owner %q does not exist", member.Owner)
			}
			acl := authorization.SchemaACLOwner(oldACL, principal, owner.Ref)
			if member.ACL != nil {
				var err error
				acl, err = restoreMemberACL(roles, member.ACL)
				if err != nil {
					return false, err
				}
				if err := authorization.ValidateACL(roles, acl, access.ClassPrivileges(d)); err != nil {
					return false, err
				}
			}
			if access.ClassPrivileges(d) != 0 && member.ACL == nil {
				return invalid()
			}
			synthetic := false
			aclShapeChanged := false
			if typ, ok := d.(*catalog.Type); ok {
				synthetic = member.ACL != nil && !member.ACL.Explicit
				aclShapeChanged = synthetic != typ.ACLSyntheticDefault
				if synthetic {
					if typ.ACLSyntheticDefault && principal == owner.Ref {
						acl = oldACL
					} else {
						acl = authorization.DefaultBaseline(owner.Ref, authorization.DefaultTypes, false)
					}
				}
			}
			if principal != owner.Ref || aclShapeChanged || oldACL.Explicit != acl.Explicit || !slices.Equal(oldACL.Entries, acl.Entries) {
				n, err := importMemberAuthority(d, owner.Ref, acl, synthetic)
				if err != nil {
					return false, err
				}
				replacements[d.Address()] = n
			}
			if typ, ok := d.(*catalog.Type); ok && record.Name == "tablefunc" && typ.SQLType.IsComposite() {
				relation, ok := view.Relation(typ.OwningRelation)
				if !ok || relation.RowType != typ.SQLType.ID() || relation.Owner != typ.Owner {
					return invalid()
				}
				if relation.Owner != owner.Ref {
					backing := *relation
					backing.Owner = owner.Ref
					replacements[backing.Address()] = &backing
				}
			}
			if typ, ok := d.(*catalog.Type); ok && typ.Array != 0 {
				array, ok := view.Type(ids.ObjectAddress{ClassID: ids.TypeClassID, ObjectID: ids.OID(typ.Array)})
				if !ok || !array.SQLType.IsArray() || array.SQLType.Element() != typ.SQLType {
					return invalid()
				}
				// A sealed array has its own mandatory declaration entry. Its
				// explicit requested authority must not be overwritten by an
				// implicit parent-owner transfer, regardless of record order.
				// Only originless generated companions follow the parent here.
				if array.Origin == nil {
					mapped := *array
					mapped.Owner = owner.Ref
					mapped.ACL = authorization.SchemaACLOwner(array.ACL, array.Owner, owner.Ref)
					if array.Owner != mapped.Owner || array.ACL.Explicit != mapped.ACL.Explicit || !slices.Equal(array.ACL.Entries, mapped.ACL.Entries) {
						replacements[array.Address()] = &mapped
					}
				}
			}
		}
		if ext != nil {
			if cohort == (ids.ObjectAddress{}) {
				cohort = ext.Installation.Cohort
			}
			if ext.Installation.Cohort != cohort {
				return invalid()
			}
			if !replay {
				if err := catalog.ValidateExtensionMembership(view, ext); err != nil {
					return invalid()
				}
				for _, a := range ext.Members {
					d := view.Get(a)
					if catalog.DescriptorOrigin(d) == nil && !ordinary[a] {
						return invalid()
					}
				}
			}
		}
		if cohort == (ids.ObjectAddress{}) || cohorts[cohort] {
			return invalid()
		}
		cohorts[cohort] = true
		// Missing survivor entries cannot silently erase authority or membership.
		for _, d := range view.Descriptors() {
			origin := catalog.DescriptorOrigin(d)
			if origin.Valid(d.Address()) && origin.Installation.Cohort == cohort && !seen[d.Address()] {
				return invalid()
			}
		}
	}
	prospective := func(a ids.ObjectAddress) catalog.Descriptor {
		if d := replacements[a]; d != nil {
			return d
		}
		return view.Get(a)
	}
	for _, d := range replacements {
		typ, ok := d.(*catalog.Type)
		if !ok {
			continue
		}
		if typ.Array != 0 {
			array, ok := prospective(ids.ObjectAddress{ClassID: ids.TypeClassID, ObjectID: ids.OID(typ.Array)}).(*catalog.Type)
			if !ok || array.Owner != typ.Owner {
				return invalid()
			}
		}
		if typ.SQLType.IsArray() {
			base, ok := prospective(ids.ObjectAddress{ClassID: ids.TypeClassID, ObjectID: ids.OID(typ.SQLType.Element().ID())}).(*catalog.Type)
			if !ok || base.Owner != typ.Owner {
				return invalid()
			}
		}
	}
	if err := ctx.Err(); err != nil {
		return false, err
	}
	if replay {
		return false, nil
	}
	for a, d := range replacements {
		view.State().SetDescriptor(a, d)
	}
	return len(replacements) != 0, nil
}

func importMemberAuthority(d catalog.Descriptor, owner authorization.PrincipalRef, acl authorization.ACLValue, synthetic bool) (catalog.Descriptor, error) {
	switch d := d.(type) {
	case *catalog.Procedure:
		n := *d
		n.Owner, n.ACL = owner, acl
		return &n, nil
	case *catalog.Type:
		n := *d
		n.Owner, n.ACL = owner, acl
		n.ACLSyntheticDefault = synthetic
		return &n, nil
	case *catalog.Language:
		n := *d
		n.Owner, n.ACL = owner, acl
		return &n, nil
	case *catalog.Operator:
		n := *d
		n.Owner = owner
		return &n, nil
	case *catalog.OperatorClass:
		n := *d
		n.Owner = owner
		return &n, nil
	case *catalog.OperatorFamily:
		n := *d
		n.Owner = owner
		return &n, nil
	case *catalog.TSDictionary:
		n := *d
		n.Owner = owner
		return &n, nil
	}
	return nil, fmt.Errorf("stuntgres: unsupported installed member authority")
}
