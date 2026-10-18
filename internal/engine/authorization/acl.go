package authorization

import "fmt"

type Privileges uint32

const (
	SelectPrivilege Privileges = 1 << iota
	InsertPrivilege
	UsagePrivilege
	CreatePrivilege
	DeletePrivilege
	UpdatePrivilege
	MaintainPrivilege
	TruncatePrivilege
	ReferencesPrivilege
	TriggerPrivilege
	ExecutePrivilege
	ConnectPrivilege
	TemporaryPrivilege
	SetPrivilege
	AlterSystemPrivilege
)

type ACLGrantee struct {
	Public bool
	Role   PrincipalRef
}
type ACLEntry struct {
	Grantor                  PrincipalRef
	Grantee                  ACLGrantee
	Privileges, GrantOptions Privileges
}
type ACLValue struct {
	Explicit bool
	Entries  []ACLEntry
}

// SchemaACLOwner follows aclnewowner: transfer both ownership references and
// combine grants that acquire the same grantor/grantee, without mutating the
// retained descriptor. NULL and explicitly empty ACLs stay distinct.
func SchemaACLOwner(acl ACLValue, old, next PrincipalRef) ACLValue {
	if !acl.Explicit || len(acl.Entries) == 0 {
		return acl
	}
	out := ACLValue{Explicit: true, Entries: append([]ACLEntry(nil), acl.Entries...)}
	newPresent := false
	for i := range out.Entries {
		entry := &out.Entries[i]
		if entry.Grantor == old {
			entry.Grantor = next
		} else if entry.Grantor == next {
			newPresent = true
		}
		if !entry.Grantee.Public {
			if entry.Grantee.Role == old {
				entry.Grantee.Role = next
			} else if entry.Grantee.Role == next {
				newPresent = true
			}
		}
	}
	if newPresent {
		dst := 0
		for i := range out.Entries {
			entry := out.Entries[i]
			if entry.Privileges|entry.GrantOptions == 0 {
				continue
			}
			for j := i + 1; j < len(out.Entries); j++ {
				other := &out.Entries[j]
				if other.Grantor == entry.Grantor && other.Grantee == entry.Grantee {
					entry.Privileges |= other.Privileges
					entry.GrantOptions |= other.GrantOptions
					other.Privileges, other.GrantOptions = 0, 0
				}
			}
			out.Entries[dst] = entry
			dst++
		}
		out.Entries = out.Entries[:dst]
	}
	return out
}
func ACLRights(acl ACLValue, owner PrincipalRef, principals []PrincipalRef, mask Privileges, options bool) Privileges {
	if !acl.Explicit {
		for _, ref := range principals {
			if ref == owner {
				return mask
			}
		}
		return 0
	}
	var rights Privileges
	for _, entry := range acl.Entries {
		allowed := entry.Grantee.Public
		if !allowed {
			for _, ref := range principals {
				if ref == entry.Grantee.Role {
					allowed = true
					break
				}
			}
		}
		if allowed {
			if options {
				rights |= entry.GrantOptions
			} else {
				rights |= entry.Privileges
			}
		}
	}
	return rights
}

// ACLItemBits converts stored privileges to PostgreSQL's aclitem bit layout.
// The two layouts intentionally differ; unsupported bits cannot silently
// disappear.
func ACLItemBits(privileges Privileges) (uint32, error) {
	var mask uint32
	for _, pair := range [...]struct {
		from Privileges
		to   uint32
	}{
		{InsertPrivilege, 1 << 0}, {SelectPrivilege, 1 << 1},
		{UpdatePrivilege, 1 << 2}, {DeletePrivilege, 1 << 3},
		{TruncatePrivilege, 1 << 4}, {ReferencesPrivilege, 1 << 5},
		{TriggerPrivilege, 1 << 6}, {ExecutePrivilege, 1 << 7},
		{UsagePrivilege, 1 << 8}, {CreatePrivilege, 1 << 9},
		{TemporaryPrivilege, 1 << 10}, {ConnectPrivilege, 1 << 11},
		{SetPrivilege, 1 << 12}, {AlterSystemPrivilege, 1 << 13},
		{MaintainPrivilege, 1 << 14},
	} {
		if privileges&pair.from != 0 {
			mask |= pair.to
			privileges &^= pair.from
		}
	}
	if privileges != 0 {
		return 0, fmt.Errorf("stuntgres: invalid stored ACL privilege")
	}
	return mask, nil
}
