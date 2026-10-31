package access

import (
	"iter"
	"stuntgres/internal/engine/authorization"
	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/ids"
)

// RoleDependency is one shared dependency on a role: ownership, a current or
// historical ACL, a policy target, or a default ACL record.
type RoleDependency struct {
	Address     ids.ObjectAddress
	Kind        byte // PostgreSQL ACL ('a'), initial ACL ('i'), owner ('o'), policy ('r').
	Description string
	PolicyTable ids.ObjectAddress
	PolicyName  string
}

// DescriptorRoleDependencies collects the dependencies of d on ref. A nil
// collector stops at the first dependency without building diagnostics.
// Admission and final publication share the exact same descriptor classification.
func DescriptorRoleDependencies(d catalog.Descriptor, ref authorization.PrincipalRef, deps *[]RoleDependency) bool {
	var lastACL ids.ObjectAddress
	aclSeen := false
	return VisitDescriptorRoleDependencies(d, func(principal authorization.PrincipalRef, dependency RoleDependency) bool {
		if principal != ref {
			return true
		}
		// An ACL has one shared dependency per role, not per ACL entry.
		// Column ACLs are separate, contiguous visits with their own sub-ID.
		if dependency.Kind == 'a' {
			if aclSeen && dependency.Address == lastACL {
				return true
			}
			lastACL, aclSeen = dependency.Address, true
		}
		return !recordRoleDependency(deps, dependency)
	})
}

// DescriptorRoleReferences uses the same dependency classification as retirement.
func DescriptorRoleReferences(d catalog.Descriptor) iter.Seq[authorization.PrincipalRef] {
	return func(yield func(authorization.PrincipalRef) bool) {
		VisitDescriptorRoleDependencies(d, func(ref authorization.PrincipalRef, _ RoleDependency) bool { return yield(ref) })
	}
}

// VisitDescriptorRoleDependencies visits canonical role dependencies without
// allocating diagnostics. ACL occurrences may repeat; policy occurrences must
// retain their multiplicity. The return value reports early termination.
func VisitDescriptorRoleDependencies(d catalog.Descriptor, yield func(authorization.PrincipalRef, RoleDependency) bool) bool {
	if relation, ok := d.(*catalog.Relation); ok {
		for _, policy := range relation.Security.Policies {
			for _, principal := range policy.Roles {
				if !principal.Public && !yield(principal.Ref, RoleDependency{Address: ids.ObjectAddress{ClassID: ids.PolicyClassID, ObjectID: policy.OID}, Kind: 'r', PolicyTable: relation.Address(), PolicyName: policy.Name}) {
					return true
				}
			}
		}
		if relation.RelationKind == catalog.IndexRelation {
			return false
		}
	}
	skipOwner := false
	if typ, ok := d.(*catalog.Type); ok {
		if typ.OwningRelation != (ids.ObjectAddress{}) {
			if !typ.SQLType.IsComposite() {
				return false
			}
			skipOwner = true
		}
		skipOwner = skipOwner || typ.SQLType.IsArray()
	}
	owner, acl, columns := DescriptorACL(d)
	if owner != (authorization.PrincipalRef{}) && !skipOwner && !yield(owner, RoleDependency{Address: d.Address(), Kind: 'o'}) {
		return true
	}
	if aclRoleReferences(d.Address(), owner, acl, yield) {
		return true
	}
	for sub, acl := range columns {
		address := d.Address()
		address.SubID = sub
		if aclRoleReferences(address, owner, acl, yield) {
			return true
		}
	}
	return false
}

func aclRoleReferences(address ids.ObjectAddress, owner authorization.PrincipalRef, acl authorization.ACLValue, yield func(authorization.PrincipalRef, RoleDependency) bool) bool {
	for _, entry := range acl.Entries {
		dependency := RoleDependency{Address: address, Kind: 'a'}
		if entry.Grantor != owner && !yield(entry.Grantor, dependency) {
			return true
		}
		if !entry.Grantee.Public && entry.Grantee.Role != owner && entry.Grantee.Role != entry.Grantor && !yield(entry.Grantee.Role, dependency) {
			return true
		}
	}
	return false
}

func DefaultACLRoleReferences(key catalog.DefaultACLKey, record *catalog.DefaultACLRecord) iter.Seq[authorization.PrincipalRef] {
	return func(yield func(authorization.PrincipalRef) bool) {
		VisitDefaultRoleDependencies(key, record, func(ref authorization.PrincipalRef, _ RoleDependency) bool { return yield(ref) })
	}
}

// VisitDefaultRoleDependencies uses the record's real catalog address.
func VisitDefaultRoleDependencies(key catalog.DefaultACLKey, record *catalog.DefaultACLRecord, yield func(authorization.PrincipalRef, RoleDependency) bool) bool {
	if record == nil {
		return false
	}
	address := ids.ObjectAddress{ClassID: 826, ObjectID: record.OID}
	if !yield(key.Creator, RoleDependency{Address: address, Kind: 'o'}) {
		return true
	}
	return aclRoleReferences(address, key.Creator, record.ACL, yield)
}

func recordRoleDependency(deps *[]RoleDependency, dependency RoleDependency) bool {
	if deps == nil {
		return true
	}
	*deps = append(*deps, dependency)
	return false
}

// DefaultRoleDependencies scans the default ACL records for dependencies on
// ref. With a nil collector it stops at the first one.
func DefaultRoleDependencies(view catalog.Read, roles authorization.Read, ref authorization.PrincipalRef, deps *[]RoleDependency) bool {
	for key, record := range view.State().DefaultACLs() {
		var dependency RoleDependency
		dependent := VisitDefaultRoleDependencies(key, record, func(principal authorization.PrincipalRef, candidate RoleDependency) bool {
			if principal != ref {
				return true
			}
			dependency = candidate
			return false
		})
		if !dependent {
			continue
		}
		if deps == nil {
			return true
		}
		creator, _ := roles.Descriptor(key.Creator)
		class := [...]string{"", "relations", "sequences", "functions", "types", "schemas", "large objects"}[key.Class]
		description := "default privileges on new " + class + " belonging to role " + creator.Name
		if key.Schema != (ids.ObjectAddress{}) {
			description += " in schema " + view.Get(key.Schema).Name()
		}
		prefix := "privileges for "
		if dependency.Kind == 'o' {
			prefix = "owner of "
		}
		dependency.Description = prefix + description
		*deps = append(*deps, dependency)
	}
	return false
}

// InitialACLRoleReferences includes historical owners as well as all other
// grantors and grantees. Current ownership is intentionally irrelevant.
func InitialACLRoleReferences(record *catalog.InitialPrivilegeRecord) iter.Seq[authorization.PrincipalRef] {
	return func(yield func(authorization.PrincipalRef) bool) {
		if record == nil {
			return
		}
		for _, entry := range record.ACL.Entries {
			if !yield(entry.Grantor) {
				return
			}
			if !entry.Grantee.Public && entry.Grantee.Role != entry.Grantor && !yield(entry.Grantee.Role) {
				return
			}
		}
	}
}

// InitialRoleDependencies has one PostgreSQL initial-ACL dependency per
// role/address, irrespective of how many entries mention that role.
func InitialRoleDependencies(view catalog.Read, ref authorization.PrincipalRef, deps *[]RoleDependency) bool {
	for address, record := range view.State().InitialPrivileges() {
		for principal := range InitialACLRoleReferences(record) {
			if principal == ref {
				if recordRoleDependency(deps, RoleDependency{Address: address, Kind: 'i'}) {
					return true
				}
				break
			}
		}
	}
	return false
}
