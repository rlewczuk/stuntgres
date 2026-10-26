package control

import (
	"stuntgres/internal/engine/access"
	"stuntgres/internal/engine/authorization"
	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/engine/pgcatalog"
	"stuntgres/internal/engine/routine"
)

// Omit only authority reconstructed exactly by the destination's system seed.
// Ordered raw entries matter: effective equality would discard user changes.
func sharedMemoryACLReconstructed(d catalog.Descriptor, bootstrap, stats authorization.PrincipalRef) bool {
	var name string
	ownerMask, statsMask := authorization.ExecutePrivilege, authorization.ExecutePrivilege
	switch object := d.(type) {
	case *catalog.Procedure:
		if object.Address().ClassID != ids.ProcedureClassID || object.Builtin == nil || object.Builtin != routine.BuiltinRoutine(object.Address().ObjectID) {
			return false
		}
		switch object.Address().ObjectID {
		case 5052:
			name = "pg_get_shmem_allocations"
		case 4100:
			name = "pg_get_shmem_allocations_numa"
		default:
			return false
		}
	case *catalog.Relation:
		if object.Address().ClassID != ids.RelationClassID || object.RelationKind != catalog.CatalogRelation || !object.VirtualView || len(object.Security.ColumnACL) != 0 {
			return false
		}
		switch object.Address().ObjectID {
		case pgcatalog.ShmemAllocationsOID:
			name = "pg_shmem_allocations"
		case pgcatalog.ShmemAllocationsNUMAOID:
			name = "pg_shmem_allocations_numa"
		default:
			return false
		}
		ownerMask, statsMask = authorization.TableGrantPrivileges, authorization.SelectPrivilege
	default:
		return false
	}
	owner, acl, _ := access.DescriptorACL(d)
	if owner != bootstrap || d.Name() != name || d.Namespace() != (ids.ObjectAddress{ClassID: ids.NamespaceClassID, ObjectID: ids.PGCatalogNamespaceOID}) || !acl.Explicit || len(acl.Entries) != 2 {
		return false
	}
	return acl.Entries[0] == (authorization.ACLEntry{Grantor: bootstrap, Grantee: authorization.ACLGrantee{Role: bootstrap}, Privileges: ownerMask}) &&
		acl.Entries[1] == (authorization.ACLEntry{Grantor: bootstrap, Grantee: authorization.ACLGrantee{Role: stats}, Privileges: statsMask})
}
