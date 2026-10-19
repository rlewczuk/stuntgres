package access

import (
	"stuntgres/internal/engine/authorization"
	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/ids"
)

// RoleDependency is one shared dependency on a role: an object it owns, an
// ACL that names it, a policy that applies to it, or a default ACL record.
type RoleDependency struct {
	Address     ids.ObjectAddress
	Kind        byte // PostgreSQL shared dependency types: ACL ('a'), owner ('o').
	Description string
	PolicyTable ids.ObjectAddress
	PolicyName  string
}

// DescriptorRoleDependencies collects the dependencies of d on ref. A nil
// collector stops at the first dependency without building diagnostics.
// Admission and final publication share the exact same descriptor classification.
func DescriptorRoleDependencies(d catalog.Descriptor, ref authorization.PrincipalRef, deps *[]RoleDependency) bool {
	if relation, ok := d.(*catalog.Relation); ok {
		for _, policy := range relation.Security.Policies {
			for _, role := range policy.Roles {
				if !role.Public && role.Ref == ref {
					if recordRoleDependency(deps, RoleDependency{Address: ids.ObjectAddress{ClassID: ids.PolicyClassID, ObjectID: policy.OID}, Kind: 'r', PolicyTable: relation.Address(), PolicyName: policy.Name}) {
						return true
					}
				}
			}
		}
	}
	if typ, ok := d.(*catalog.Type); ok && typ.OwningRelation != (ids.ObjectAddress{}) {
		// Dependent row/array ownership belongs to the relation; independent
		// composite types still carry their own named ACL dependency.
		if !typ.SQLType.IsComposite() {
			return false
		}
		if typ.Owner == ref {
			return false
		}
	}
	if relation, ok := d.(*catalog.Relation); ok && relation.RelationKind == catalog.IndexRelation {
		// Index ownership belongs to its validated parent relation.
		return false
	}
	owner, acl, columns := DescriptorACL(d)
	if owner == ref {
		if typ, array := d.(*catalog.Type); array && typ.SQLType.IsArray() {
			// Associated array ownership is represented by its parent.
			return false
		}
		// The owner's shared ACL dependencies are represented by ownership.
		return recordRoleDependency(deps, RoleDependency{Address: d.Address(), Kind: 'o'})
	}
	if aclRoleDependencies(d.Address(), acl, ref, deps) {
		return true
	}
	for sub, acl := range columns {
		address := d.Address()
		address.SubID = sub
		if aclRoleDependencies(address, acl, ref, deps) {
			return true
		}
	}
	return false
}

func aclRoleDependencies(address ids.ObjectAddress, acl authorization.ACLValue, ref authorization.PrincipalRef, deps *[]RoleDependency) bool {
	for _, entry := range acl.Entries {
		if entry.Grantor == ref || !entry.Grantee.Public && entry.Grantee.Role == ref {
			return recordRoleDependency(deps, RoleDependency{Address: address, Kind: 'a'})
		}
	}
	return false
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
		kind := byte('a')
		dependent := key.Creator == ref
		if dependent {
			kind = 'o'
		} else {
			for _, entry := range record.ACL.Entries {
				if entry.Grantor == ref || !entry.Grantee.Public && entry.Grantee.Role == ref {
					dependent = true
					break
				}
			}
		}
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
		if kind == 'o' {
			prefix = "owner of "
		}
		*deps = append(*deps, RoleDependency{Kind: kind, Description: prefix + description})
	}
	return false
}
