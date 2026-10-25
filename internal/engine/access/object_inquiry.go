package access

import (
	"context"

	"stuntgres/internal/engine/authorization"
	"stuntgres/internal/engine/ids"
)

// ObjectPrivilege is the SQL inquiry boundary, not creation or execution
// admission. A raw nonexistent role OID still receives PUBLIC rights, and an
// absent language ACL has PUBLIC USAGE even for an untrusted language.
func (v View) ObjectPrivilege(ctx context.Context, actor uint64, address ids.ObjectAddress, ordinary, options bool) (value, exists bool, err error) {
	principal := v.Owner.Principals.Principal(actor)
	role, live := v.Roles.Find(principal)
	if live && role.Superuser { return true, true, nil }
	if _, present := v.Catalog.Descriptor(address); !present { return false, false, nil }
	d, err := v.Object(v.Ref(address))
	if err != nil { return false, false, err }
	owner, acl, _ := DescriptorACL(d)
	mask := ClassPrivileges(d)
	var principals []authorization.PrincipalRef
	if live {
		principals, err = v.Roles.RoleClosure(ctx, principal, true)
		if err != nil { return false, false, err }
	}
	if options {
		for _, p := range principals {
			if p == owner { return true, true, nil }
		}
		if authorization.ACLRights(acl, owner, principals, mask, true)&mask != 0 { return true, true, nil }
	}
	if ordinary {
		rights := authorization.ACLRights(acl, owner, principals, mask, false)
		if !acl.Explicit && (address.ClassID == ids.ProcedureClassID || address.ClassID == ids.LanguageClassID) { rights |= mask }
		if rights&mask != 0 { return true, true, nil }
	}
	return false, true, nil
}
