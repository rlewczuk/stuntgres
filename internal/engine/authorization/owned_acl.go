package authorization

import "context"

// ALL/CASCADE revokes every selected grantee and grantor provenance using the
// ordinary selective ACL planner, preserving independently supported grants.
func OwnedACL(ctx context.Context, roles Read, acl ACLValue, owner PrincipalRef, sources map[PrincipalRef]bool) (ACLValue, bool, error) {
	next := acl
	changed := false
	for _, entry := range acl.Entries {
		if err := ctx.Err(); err != nil {
			return acl, false, err
		}
		// Retire received rights first. Recursive revocation can still find
		// inherited owner or independently inherited grant-option support.
		if entry.Grantee.Public || !sources[entry.Grantee.Role] {
			continue
		}
		candidate, different, err := ChangeObjectACL(ctx, roles, next, owner, entry.Grantor, []ACLGrantee{entry.Grantee}, entry.Privileges|entry.GrantOptions, false, true, false, true)
		if err != nil {
			return acl, false, err
		}
		next = candidate
		changed = changed || different
	}
	for _, entry := range next.Entries {
		if err := ctx.Err(); err != nil {
			return acl, false, err
		}
		if !sources[entry.Grantor] || !entry.Grantee.Public && sources[entry.Grantee.Role] {
			continue
		}
		mask := entry.Privileges | entry.GrantOptions
		supported, err := ScopedACLGrantOptions(ctx, roles, next, owner, entry.Grantor, mask)
		if err != nil {
			return acl, false, err
		}
		mask &^= supported
		if mask == 0 {
			continue
		}
		candidate, different, err := ChangeObjectACL(ctx, roles, next, owner, entry.Grantor, []ACLGrantee{entry.Grantee}, mask, false, true, false, true)
		if err != nil {
			return acl, false, err
		}
		next = candidate
		changed = changed || different
	}
	return next, changed, nil
}
