package authorization

import (
	"cmp"
	"fmt"
	"slices"
	"strings"
	"stuntgres/internal/parser"
)

type DefaultACLClass uint8

const (
	DefaultTables DefaultACLClass = iota + 1
	DefaultSequences
	DefaultRoutines
	DefaultTypes
	DefaultSchemas
	DefaultLargeObjects
	// These masks serve object grants, not ALTER DEFAULT PRIVILEGES targets.
	DefaultParameters
	DefaultTablespaces
)

func DefaultClass(name string) DefaultACLClass {
	switch name {
	case "tables":
		return DefaultTables
	case "sequences":
		return DefaultSequences
	case "functions", "routines":
		return DefaultRoutines
	case "types":
		return DefaultTypes
	case "schemas":
		return DefaultSchemas
	case "large objects":
		return DefaultLargeObjects
	}
	return 0
}
func DefaultMask(class DefaultACLClass) Privileges {
	switch class {
	case DefaultTables:
		return TableGrantPrivileges
	case DefaultSequences:
		return SelectPrivilege | UsagePrivilege | UpdatePrivilege
	case DefaultRoutines:
		return ExecutePrivilege
	case DefaultTypes:
		return UsagePrivilege
	case DefaultSchemas:
		return UsagePrivilege | CreatePrivilege
	case DefaultLargeObjects:
		return SelectPrivilege | UpdatePrivilege
	case DefaultParameters:
		return SetPrivilege | AlterSystemPrivilege
	case DefaultTablespaces:
		return CreatePrivilege
	}
	return 0
}
func defaultKind(class DefaultACLClass) string {
	switch class {
	case DefaultTables:
		return "relation"
	case DefaultSequences:
		return "sequence"
	case DefaultRoutines:
		return "function"
	case DefaultTypes:
		return "type"
	case DefaultSchemas:
		return "schema"
	case DefaultLargeObjects:
		return "large object"
	case DefaultParameters:
		return "parameter"
	case DefaultTablespaces:
		return "tablespace"
	}
	return ""
}
func DefaultBaseline(owner PrincipalRef, class DefaultACLClass, scoped bool) ACLValue {
	if scoped {
		return ACLValue{Explicit: true}
	}
	entry := ACLEntry{Grantor: owner, Grantee: ACLGrantee{Role: owner}, Privileges: DefaultMask(class)}
	if class == DefaultTypes || class == DefaultRoutines {
		return ACLValue{Explicit: true, Entries: []ACLEntry{entry, {Grantor: owner, Grantee: ACLGrantee{Public: true}, Privileges: DefaultMask(class)}}}
	}
	return ACLValue{Explicit: true, Entries: []ACLEntry{entry}}
}
func MatchesDefaultBaseline(acl ACLValue, owner PrincipalRef, class DefaultACLClass, scoped bool) bool {
	if !acl.Explicit {
		return false
	}
	if scoped {
		return len(acl.Entries) == 0
	}
	public := class == DefaultTypes || class == DefaultRoutines
	count := 1
	if public {
		count = 2
	}
	if len(acl.Entries) != count {
		return false
	}
	seenOwner, seenPublic := false, false
	for _, entry := range acl.Entries {
		if entry.Grantor != owner || entry.Privileges != DefaultMask(class) || entry.GrantOptions != 0 {
			return false
		}
		if entry.Grantee.Public {
			if !public || seenPublic || entry.Grantee.Role != (PrincipalRef{}) {
				return false
			}
			seenPublic = true
		} else {
			if seenOwner || entry.Grantee.Role != owner {
				return false
			}
			seenOwner = true
		}
	}
	return seenOwner && seenPublic == public
}
func EqualACL(a, b ACLValue) bool {
	if a.Explicit != b.Explicit || len(a.Entries) != len(b.Entries) {
		return false
	}
	for _, entry := range a.Entries {
		found := false
		for _, other := range b.Entries {
			if entry == other {
				found = true
				break
			}
		}
		if !found {
			return false
		}
	}
	return true
}
func DefaultPrivilege(item parser.GrantPrivilege, class DefaultACLClass) (Privileges, error) {
	if item.Columns != nil {
		return 0, sqlError("0LP01", "default privileges cannot be set for columns")
	}
	if item.All {
		return DefaultMask(class), nil
	}
	var bit Privileges
	switch item.Name {
	case "select":
		bit = SelectPrivilege
	case "insert":
		bit = InsertPrivilege
	case "update":
		bit = UpdatePrivilege
	case "delete":
		bit = DeletePrivilege
	case "truncate":
		bit = TruncatePrivilege
	case "references":
		bit = ReferencesPrivilege
	case "trigger":
		bit = TriggerPrivilege
	case "maintain":
		bit = MaintainPrivilege
	case "usage":
		bit = UsagePrivilege
	case "create":
		bit = CreatePrivilege
	case "execute":
		bit = ExecutePrivilege
	case "connect", "temporary", "temp", "set", "alter system":
	default:
		return 0, sqlError("42601", fmt.Sprintf(`unrecognized privilege type "%s"`, item.Name))
	}
	if bit == 0 || bit & ^DefaultMask(class) != 0 {
		name := strings.ToUpper(item.Name)
		if name == "TEMPORARY" {
			name = "TEMP"
		}
		return 0, sqlError("0LP01", "invalid privilege type "+name+" for "+defaultKind(class))
	}
	return bit, nil
}

// Creation alone performs release aclmerge normalization. Access to an ordinary
// explicitly empty object ACL must never turn it into hardwired owner rights.
func CreationDefaultACL(owner PrincipalRef, class DefaultACLClass, record, scoped *ACLValue) ACLValue {
	if record == nil && scoped == nil {
		if class == DefaultTypes {
			return DefaultBaseline(owner, class, false)
		}
		return ACLValue{}
	}
	var global ACLValue
	if record != nil {
		global = *record
	} else {
		global = DefaultBaseline(owner, class, false)
	}
	merged := global
	if scoped != nil && len(scoped.Entries) != 0 {
		merged = ACLValue{Explicit: true, Entries: make([]ACLEntry, len(global.Entries), len(global.Entries)+len(scoped.Entries))}
		copy(merged.Entries, global.Entries)
		for _, entry := range scoped.Entries {
			found := false
			for i := range merged.Entries {
				existing := &merged.Entries[i]
				if existing.Grantor == entry.Grantor && existing.Grantee == entry.Grantee {
					existing.Privileges |= entry.Privileges
					existing.GrantOptions |= entry.GrantOptions
					found = true
					break
				}
			}
			if !found {
				merged.Entries = append(merged.Entries, entry)
			}
		}
	}
	if len(merged.Entries) == 0 || MatchesDefaultBaseline(merged, owner, class, false) {
		if class == DefaultTypes {
			return DefaultBaseline(owner, class, false)
		}
		return ACLValue{}
	}
	if (class == DefaultSchemas || class == DefaultLargeObjects) && len(merged.Entries) > 1 {
		compare := func(a, b ACLEntry) int {
			grantee := func(entry ACLEntry) uint64 {
				if entry.Grantee.Public {
					return 0
				}
				return entry.Grantee.Role.ID()
			}
			if order := cmp.Compare(grantee(a), grantee(b)); order != 0 {
				return order
			}
			return cmp.Compare(a.Grantor.ID(), b.Grantor.ID())
		}
		if !slices.IsSortedFunc(merged.Entries, compare) {
			merged.Entries = slices.Clone(merged.Entries)
			slices.SortFunc(merged.Entries, compare)
		}
	}
	return merged
}
func ValidateDefaultACLHeader(roles Read, creator PrincipalRef, class DefaultACLClass, scoped bool, acl ACLValue) error {
	if !acl.Explicit || DefaultMask(class) == 0 {
		return fmt.Errorf("stuntgres: invalid default ACL record")
	}
	if _, err := roles.Descriptor(creator); err != nil {
		return err
	}
	if scoped && (class == DefaultSchemas || class == DefaultLargeObjects) {
		return fmt.Errorf("stuntgres: invalid scoped default ACL")
	}
	return nil
}
func ValidateDefaultACL(roles Read, creator PrincipalRef, class DefaultACLClass, scoped bool, acl ACLValue) error {
	if MatchesDefaultBaseline(acl, creator, class, scoped) {
		return fmt.Errorf("stuntgres: redundant default ACL record")
	}
	for i, entry := range acl.Entries {
		if entry.Grantor != creator || entry.Privileges == 0 || entry.Privileges & ^DefaultMask(class) != 0 || entry.GrantOptions & ^entry.Privileges != 0 {
			return fmt.Errorf("stuntgres: invalid default ACL entry")
		}
		for _, previous := range acl.Entries[:i] {
			if previous.Grantor == entry.Grantor && previous.Grantee == entry.Grantee {
				return fmt.Errorf("stuntgres: duplicate default ACL origin")
			}
		}
		if entry.Grantee.Public {
			if entry.Grantee.Role != (PrincipalRef{}) || entry.GrantOptions != 0 {
				return fmt.Errorf("stuntgres: invalid PUBLIC default ACL")
			}
		} else if _, err := roles.Descriptor(entry.Grantee.Role); err != nil {
			return err
		}
	}
	return nil
}
