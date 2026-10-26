package routine

import (
	"stuntgres/internal/engine/authorization"
	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/expr"
	"stuntgres/internal/engine/ids"
)

// Exact PostgreSQL declarations for view EXECUTE authority and catalog metadata.
// Zero provider flags deliberately leave ordinary routine execution unavailable.
var sharedMemoryDeclarations = [...]catalog.BuiltinRoutineDeclaration{
	{Identity: catalog.RoutineIdentity{ID: 5052, Namespace: ids.PGCatalogNamespaceOID, Name: "pg_get_shmem_allocations"}, Kind: catalog.FunctionRoutine, Result: 2249, Strict: true, Volatility: expr.Volatile, Parallel: 's', ReturnsSet: true, Outputs: catalog.NewRoutineOutputs([]ids.OID{25, 20, 20, 20}, "name", "off", "size", "allocated_size")},
	{Identity: catalog.RoutineIdentity{ID: 4100, Namespace: ids.PGCatalogNamespaceOID, Name: "pg_get_shmem_allocations_numa"}, Kind: catalog.FunctionRoutine, Result: 2249, Strict: true, Volatility: expr.Volatile, Parallel: 's', ReturnsSet: true, Outputs: catalog.NewRoutineOutputs([]ids.OID{25, 23, 20}, "name", "numa_node", "size")},
}

// SharedMemoryProcedures creates engine-owned ACL descriptors, without adding
// the declarations to a callable provider or scalar-selector inventory.
func SharedMemoryProcedures(owner, stats authorization.PrincipalRef) []catalog.Procedure {
	procedures := make([]catalog.Procedure, len(sharedMemoryDeclarations))
	for i := range sharedMemoryDeclarations {
		declaration := &sharedMemoryDeclarations[i]
		procedures[i] = catalog.Procedure{
			ObjectAddress: ids.ObjectAddress{ClassID: ids.ProcedureClassID, ObjectID: declaration.Identity.ID},
			ObjectNamespace: ids.ObjectAddress{ClassID: ids.NamespaceClassID, ObjectID: ids.PGCatalogNamespaceOID},
			ObjectRevision: 1,
			ObjectName: declaration.Identity.Name,
			RoutineKind: declaration.Kind,
			Builtin: declaration,
			Owner: owner,
			Result: declaration.Result,
			Volatility: declaration.Volatility,
			Strict: declaration.Strict,
			Display: catalog.RoutineDisplay{Language: catalog.InternalRoutineLanguage, Source: declaration.Identity.Name},
			ACL: authorization.ACLValue{Explicit: true, Entries: []authorization.ACLEntry{
				{Grantor: owner, Grantee: authorization.ACLGrantee{Role: owner}, Privileges: authorization.ExecutePrivilege},
				{Grantor: owner, Grantee: authorization.ACLGrantee{Role: stats}, Privileges: authorization.ExecutePrivilege},
			}},
		}
	}
	return procedures
}
