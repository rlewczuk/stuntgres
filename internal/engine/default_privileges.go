package engine

import (
	"fmt"
	"strings"

	"stuntgres/internal/parser"
)

type defaultACLClass uint8

const (
	defaultTables defaultACLClass = iota + 1
	defaultSequences
	defaultRoutines
	defaultTypes
	defaultSchemas
	defaultLargeObjects
)

type defaultACLKey struct {
	creator PrincipalRef
	schema  ObjectAddress
	class   defaultACLClass
}
type defaultACLRecord struct{ acl ACLValue }

func defaultClass(name string) defaultACLClass {
	switch name {
	case "tables":
		return defaultTables
	case "sequences":
		return defaultSequences
	case "functions", "routines":
		return defaultRoutines
	case "types":
		return defaultTypes
	case "schemas":
		return defaultSchemas
	case "large objects":
		return defaultLargeObjects
	}
	return 0
}
func defaultMask(class defaultACLClass) Privileges {
	switch class {
	case defaultTables:
		return tableGrantPrivileges
	case defaultSequences:
		return SelectPrivilege | UsagePrivilege | UpdatePrivilege
	case defaultRoutines:
		return ExecutePrivilege
	case defaultTypes:
		return UsagePrivilege
	case defaultSchemas:
		return UsagePrivilege | CreatePrivilege
	case defaultLargeObjects:
		return SelectPrivilege | UpdatePrivilege
	}
	return 0
}
func defaultKind(class defaultACLClass) string {
	switch class {
	case defaultTables:
		return "relation"
	case defaultSequences:
		return "sequence"
	case defaultRoutines:
		return "function"
	case defaultTypes:
		return "type"
	case defaultSchemas:
		return "schema"
	case defaultLargeObjects:
		return "large object"
	}
	return ""
}
func defaultBaseline(owner PrincipalRef, class defaultACLClass, scoped bool) ACLValue {
	if scoped {
		return ACLValue{Explicit: true}
	}
	entry := ACLEntry{Grantor: owner, Grantee: ACLGrantee{Role: owner}, Privileges: defaultMask(class)}
	if class == defaultTypes || class == defaultRoutines {
		return ACLValue{Explicit: true, Entries: []ACLEntry{entry, {Grantor: owner, Grantee: ACLGrantee{Public: true}, Privileges: defaultMask(class)}}}
	}
	return ACLValue{Explicit: true, Entries: []ACLEntry{entry}}
}
func matchesDefaultBaseline(acl ACLValue, owner PrincipalRef, class defaultACLClass, scoped bool) bool {
	if !acl.Explicit {
		return false
	}
	if scoped {
		return len(acl.Entries) == 0
	}
	public := class == defaultTypes || class == defaultRoutines
	count := 1
	if public {
		count = 2
	}
	if len(acl.Entries) != count {
		return false
	}
	seenOwner, seenPublic := false, false
	for _, entry := range acl.Entries {
		if entry.Grantor != owner || entry.Privileges != defaultMask(class) || entry.GrantOptions != 0 {
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
func equalACL(a, b ACLValue) bool {
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
func defaultPrivilege(item parser.GrantPrivilege, class defaultACLClass) (Privileges, error) {
	if item.Columns != nil {
		return 0, sqlError("0LP01", "default privileges cannot be set for columns")
	}
	if item.All {
		return defaultMask(class), nil
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
	if bit == 0 || bit & ^defaultMask(class) != 0 {
		name := strings.ToUpper(item.Name)
		if name == "TEMPORARY" {
			name = "TEMP"
		}
		return 0, sqlError("0LP01", "invalid privilege type "+name+" for "+defaultKind(class))
	}
	return bit, nil
}
func cloneDefaultACLs(source map[defaultACLKey]*defaultACLRecord) map[defaultACLKey]*defaultACLRecord {
	next := make(map[defaultACLKey]*defaultACLRecord, len(source))
	for key, record := range source {
		next[key] = record
	}
	return next
}
func overlayDefaultACLs(source map[defaultACLKey]*defaultACLRecord, changes map[defaultACLKey]*defaultACLRecord) map[defaultACLKey]*defaultACLRecord {
	if len(changes) == 0 {
		return source
	}
	next := cloneDefaultACLs(source)
	for key, record := range changes {
		if record == nil {
			delete(next, key)
		} else {
			next[key] = record
		}
	}
	return next
}

// Creation alone performs release aclmerge normalization. Access to an ordinary
// explicitly empty object ACL must never turn it into hardwired owner rights.
func creationDefaultACL(view CatalogRead, owner PrincipalRef, schema ObjectAddress, class defaultACLClass) ACLValue {
	record := view.state.defaultACLs[defaultACLKey{creator: owner, class: class}]
	var scoped *defaultACLRecord
	if schema != (ObjectAddress{}) {
		scoped = view.state.defaultACLs[defaultACLKey{creator: owner, schema: schema, class: class}]
	}
	if record == nil && scoped == nil {
		if class == defaultTypes {
			return defaultBaseline(owner, class, false)
		}
		return ACLValue{}
	}
	var global ACLValue
	if record != nil {
		global = record.acl
	} else {
		global = defaultBaseline(owner, class, false)
	}
	merged := global
	if scoped != nil && len(scoped.acl.Entries) != 0 {
		merged = ACLValue{Explicit: true, Entries: make([]ACLEntry, len(global.Entries), len(global.Entries)+len(scoped.acl.Entries))}
		copy(merged.Entries, global.Entries)
		for _, entry := range scoped.acl.Entries {
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
	if len(merged.Entries) == 0 || matchesDefaultBaseline(merged, owner, class, false) {
		if class == defaultTypes {
			return defaultBaseline(owner, class, false)
		}
		return ACLValue{}
	}
	return merged
}

func validateDefaultACLs(roles RoleRead, view CatalogRead) error {
	for key, record := range view.state.defaultACLs {
		if record == nil || !record.acl.Explicit || defaultMask(key.class) == 0 {
			return fmt.Errorf("stuntgres: invalid default ACL record")
		}
		if _, err := roles.Descriptor(key.creator); err != nil {
			return err
		}
		if key.schema != (ObjectAddress{}) {
			if key.class == defaultSchemas || key.class == defaultLargeObjects {
				return fmt.Errorf("stuntgres: invalid scoped default ACL")
			}
			if _, ok := view.state.descriptors[key.schema].(*namespaceDescriptor); !ok {
				return CatalogConflict{}
			}
		}
		if matchesDefaultBaseline(record.acl, key.creator, key.class, key.schema != (ObjectAddress{})) {
			return fmt.Errorf("stuntgres: redundant default ACL record")
		}
		for i, entry := range record.acl.Entries {
			if entry.Grantor != key.creator || entry.Privileges == 0 || entry.Privileges & ^defaultMask(key.class) != 0 || entry.GrantOptions & ^entry.Privileges != 0 {
				return fmt.Errorf("stuntgres: invalid default ACL entry")
			}
			for _, previous := range record.acl.Entries[:i] {
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
	}
	return nil
}
