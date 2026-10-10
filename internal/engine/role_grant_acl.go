package engine

import (
	"context"
	"math/bits"
	"slices"
	"stuntgres/internal/sqlstate"
)

const tableGrantPrivileges = SelectPrivilege | InsertPrivilege | UpdatePrivilege | DeletePrivilege | TruncatePrivilege | ReferencesPrivilege | TriggerPrivilege | MaintainPrivilege
const columnGrantPrivileges = SelectPrivilege | InsertPrivilege | UpdatePrivilege | ReferencesPrivilege

func materializeRelationACL(acl ACLValue, owner PrincipalRef) ACLValue {
	if acl.Explicit {
		return acl
	}
	return ACLValue{Explicit: true, Entries: []ACLEntry{{Grantor: owner, Grantee: ACLGrantee{Role: owner}, Privileges: tableGrantPrivileges}}}
}

// Choose one direct source, never a union of independently inherited sources.
// Closure order is breadth-first, providing the nearest source on equal masks.
func objectGrantSource(ctx context.Context, owner PrincipalRef, table, column ACLValue, principals []PrincipalRef, super bool, requested Privileges) (PrincipalRef, Privileges, error) {
	for _, role := range principals {
		if err := ctx.Err(); err != nil {
			return PrincipalRef{}, 0, err
		}
		if super || role == owner {
			return owner, requested, nil
		}
	}
	source := principals[0]
	var available Privileges
	for _, role := range principals {
		if err := ctx.Err(); err != nil {
			return PrincipalRef{}, 0, err
		}
		var options Privileges
		for _, acl := range []ACLValue{table, column} {
			for i, entry := range acl.Entries {
				if i&255 == 0 {
					if err := ctx.Err(); err != nil {
						return PrincipalRef{}, 0, err
					}
				}
				if !entry.Grantee.Public && entry.Grantee.Role == role {
					options |= entry.GrantOptions
				}
			}
		}
		options &= requested
		if bits.OnesCount32(uint32(options)) > bits.OnesCount32(uint32(available)) {
			source, available = role, options
		}
		if available == requested {
			break
		}
	}
	return source, available, nil
}

func scopedACLGrantOptions(ctx context.Context, roles RoleRead, acl ACLValue, owner, role PrincipalRef) (Privileges, error) {
	principals, err := roles.roleClosure(ctx, role, true)
	if err != nil {
		return 0, err
	}
	for _, principal := range principals {
		if principal == owner {
			return tableGrantPrivileges, nil
		}
	}
	return aclRights(acl, PrincipalRef{}, principals, tableGrantPrivileges, true), nil
}

// Temporary recursive removal mirrors PostgreSQL's per-ACL support check.
// It is used for circularity only; public CASCADE is not implemented.
func cascadeACLBits(ctx context.Context, roles RoleRead, acl *ACLValue, owner, role PrincipalRef, lost Privileges) error {
	if role == owner {
		return nil
	}
	options, err := scopedACLGrantOptions(ctx, roles, *acl, owner, role)
	if err != nil {
		return err
	}
	lost &^= options
	if lost == 0 {
		return nil
	}
	for i := range acl.Entries {
		if err := ctx.Err(); err != nil {
			return err
		}
		entry := &acl.Entries[i]
		if entry.Grantor != role {
			continue
		}
		removed := entry.GrantOptions & lost
		grantee := entry.Grantee
		entry.Privileges &^= lost
		entry.GrantOptions &^= lost
		if !grantee.Public && removed != 0 {
			if err := cascadeACLBits(ctx, roles, acl, owner, grantee.Role, removed); err != nil {
				return err
			}
		}
	}
	return nil
}

func checkACLGrantCycle(ctx context.Context, roles RoleRead, acl ACLValue, owner, source PrincipalRef, grantee ACLGrantee, mask Privileges) error {
	if grantee.Public || grantee.Role == owner || source == owner || mask == 0 {
		return nil
	}
	candidate := ACLValue{Explicit: true, Entries: slices.Clone(acl.Entries)}
	var removed Privileges
	for i := range candidate.Entries {
		entry := &candidate.Entries[i]
		if entry.Grantee == grantee {
			removed |= entry.GrantOptions
			entry.GrantOptions = 0
		}
	}
	if err := cascadeACLBits(ctx, roles, &candidate, owner, grantee.Role, removed); err != nil {
		return err
	}
	options, err := scopedACLGrantOptions(ctx, roles, candidate, owner, source)
	if err != nil {
		return err
	}
	if mask&^options != 0 {
		return sqlError("0LP01", "grant options cannot be granted back to your own grantor")
	}
	return nil
}

func changeObjectACL(ctx context.Context, roles RoleRead, old ACLValue, owner, source PrincipalRef, grantees []ACLGrantee, mask Privileges, option, revoke bool) (ACLValue, bool, error) {
	if mask == 0 {
		return old, false, nil
	}
	needsChange := false
	for _, grantee := range grantees {
		if err := ctx.Err(); err != nil {
			return old, false, err
		}
		found := false
		for _, entry := range old.Entries {
			if entry.Grantor != source || entry.Grantee != grantee {
				continue
			}
			found = true
			if revoke {
				needsChange = needsChange || entry.Privileges&mask != 0
			} else {
				needsChange = needsChange || entry.Privileges&mask != mask || option && entry.GrantOptions&mask != mask
			}
		}
		if !revoke && !found {
			needsChange = true
		}
	}
	if !needsChange {
		return old, false, nil
	}
	// Check cycles against each preceding private update, preserving input order.
	candidate := ACLValue{Explicit: true, Entries: slices.Clone(old.Entries)}
	for _, grantee := range grantees {
		if err := ctx.Err(); err != nil {
			return old, false, err
		}
		if !revoke && option {
			if err := checkACLGrantCycle(ctx, roles, candidate, owner, source, grantee, mask); err != nil {
				return old, false, err
			}
		}
		found := -1
		for i, entry := range candidate.Entries {
			if entry.Grantor == source && entry.Grantee == grantee {
				found = i
				break
			}
		}
		if revoke {
			if found < 0 {
				continue
			}
			entry := &candidate.Entries[found]
			removed := entry.GrantOptions & mask
			entry.Privileges &^= mask
			entry.GrantOptions &^= mask
			if !grantee.Public && removed != 0 && grantee.Role != owner {
				options, err := scopedACLGrantOptions(ctx, roles, candidate, owner, grantee.Role)
				if err != nil {
					return old, false, err
				}
				lost := removed &^ options
				for _, dependent := range candidate.Entries {
					if dependent.Grantor == grantee.Role && dependent.Privileges&lost != 0 {
						return old, false, &sqlstate.Error{Severity: "ERROR", SeverityUnlocalized: "ERROR", Code: "2BP01", Message: "dependent privileges exist", Hint: "Use CASCADE to revoke them too."}
					}
				}
			}
		} else if found < 0 {
			entry := ACLEntry{Grantor: source, Grantee: grantee, Privileges: mask}
			if option {
				entry.GrantOptions = mask
			}
			candidate.Entries = append(candidate.Entries, entry)
		} else {
			candidate.Entries[found].Privileges |= mask
			if option {
				candidate.Entries[found].GrantOptions |= mask
			}
		}
	}
	n := 0
	for _, entry := range candidate.Entries {
		if entry.Privileges != 0 {
			candidate.Entries[n] = entry
			n++
		}
	}
	candidate.Entries = candidate.Entries[:n]
	if old.Explicit == candidate.Explicit && slices.Equal(old.Entries, candidate.Entries) {
		return old, false, nil
	}
	return candidate, true, nil
}
