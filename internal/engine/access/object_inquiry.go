package access

import (
	"context"

	"stuntgres/internal/engine/authorization"
	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/ids"
)

// ObjectPrivilege is the raw-OID SQL inquiry boundary, not admission. Missing
// actors still receive PUBLIC rights; live superusers bypass object existence.
// env supplies only the physical session's own temporary namespace.
func (v View) ObjectPrivilege(ctx context.Context, env Environment, actor uint64, address ids.ObjectAddress, ordinary, options authorization.Privileges) (value, exists bool, err error) {
	principal := v.Owner.Principals.Principal(actor)
	role, live := v.Roles.Find(principal)
	if live && role.Superuser {
		return true, true, nil
	}
	if _, present := v.Catalog.Descriptor(address); !present {
		return false, false, nil
	}
	d, err := v.Object(v.Ref(address))
	if err != nil {
		return false, false, err
	}
	if typ, ok := d.(*catalog.Type); ok {
		d, err = catalog.TypeUsageAuthority(v.Catalog, typ)
		if err != nil {
			return false, false, err
		}
	}
	if ns, ok := d.(*catalog.Namespace); ok && ownsTemporaryNamespace(env, ns) {
		rights := authorization.UsagePrivilege
		temporary, _, err := v.ObjectPrivilege(ctx, env, actor, v.Owner.DatabaseAddress(), authorization.TemporaryPrivilege, 0)
		if err != nil {
			return false, false, err
		}
		if temporary {
			rights |= authorization.CreatePrivilege
		}
		return rights&ordinary != 0, true, nil
	}
	owner, acl, _ := DescriptorACL(d)
	mask := ClassPrivileges(d)
	var principals []authorization.PrincipalRef
	if live {
		principals, err = v.Roles.RoleClosure(ctx, principal, true)
		if err != nil {
			return false, false, err
		}
	}
	if options != 0 {
		for _, p := range principals {
			if p == owner && options&mask != 0 {
				return true, true, nil
			}
		}
		if authorization.ACLRights(acl, owner, principals, mask, true)&options != 0 {
			return true, true, nil
		}
	}
	if ordinary != 0 {
		rights := authorization.ACLRights(acl, owner, principals, mask, false)
		if !acl.Explicit {
			switch d.Kind() {
			case ids.DatabaseObject:
				rights |= authorization.DatabasePublicPrivileges
			case ids.TypeObject:
				rights |= authorization.UsagePrivilege
			case ids.ProcedureObject, ids.LanguageObject:
				rights |= mask
			}
		}
		if live {
			rights |= PredefinedPrivileges(d, principals)
		}
		if rights&ordinary != 0 {
			return true, true, nil
		}
	}
	return false, true, nil
}
