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
func restrictedStatsACLReconstructed(d catalog.Descriptor, bootstrap, stats authorization.PrincipalRef) bool {
	var name string
	ownerMask, statsMask := authorization.ExecutePrivilege, authorization.ExecutePrivilege
	entries := 2
	switch object := d.(type) {
	case *catalog.Procedure:
		if object.Address().ClassID != ids.ProcedureClassID || object.Address().SubID != 0 || object.Builtin == nil || object.Builtin != routine.BuiltinRoutine(object.Address().ObjectID) || object.Signature != object.Builtin.Identity.Signature {
			return false
		}
		switch object.Address().ObjectID {
		case 3195:
			name, entries = "pg_stat_get_archiver", 0
		case 2306:
			name, entries = "pg_stat_get_slru", 0
		case 2772:
			name, entries = "pg_stat_get_bgwriter_buf_written_clean", 0
		case 2773:
			name, entries = "pg_stat_get_bgwriter_maxwritten_clean", 0
		case 2859:
			name, entries = "pg_stat_get_buf_alloc", 0
		case 3075:
			name, entries = "pg_stat_get_bgwriter_stat_reset_time", 0
		case 6118:
			name, entries = "pg_stat_get_subscription", 0
		case 2769:
			name, entries = "pg_stat_get_checkpointer_num_timed", 0
		case 2770:
			name, entries = "pg_stat_get_checkpointer_num_requested", 0
		case 6377:
			name, entries = "pg_stat_get_checkpointer_num_performed", 0
		case 6327:
			name, entries = "pg_stat_get_checkpointer_restartpoints_timed", 0
		case 6328:
			name, entries = "pg_stat_get_checkpointer_restartpoints_requested", 0
		case 6329:
			name, entries = "pg_stat_get_checkpointer_restartpoints_performed", 0
		case 3160:
			name, entries = "pg_stat_get_checkpointer_write_time", 0
		case 3161:
			name, entries = "pg_stat_get_checkpointer_sync_time", 0
		case 2771:
			name, entries = "pg_stat_get_checkpointer_buffers_written", 0
		case 6366:
			name, entries = "pg_stat_get_checkpointer_slru_written", 0
		case 6314:
			name, entries = "pg_stat_get_checkpointer_stat_reset_time", 0
		case 3046:
			name, entries = "pg_stat_get_xact_function_calls", 0
		case 3047:
			name, entries = "pg_stat_get_xact_function_total_time", 0
		case 3048:
			name, entries = "pg_stat_get_xact_function_self_time", 0
		case 2978:
			name, entries = "pg_stat_get_function_calls", 0
		case 2979:
			name, entries = "pg_stat_get_function_total_time", 0
		case 2980:
			name, entries = "pg_stat_get_function_self_time", 0
		case 5052:
			name = "pg_get_shmem_allocations"
		case 4100:
			name = "pg_get_shmem_allocations_numa"
		case 6399:
			name = "pg_get_aios"
		case 2282:
			name = "pg_get_backend_memory_contexts"
		case 3400:
			name, entries = "pg_config", 1
		case 3329:
			name, entries = "pg_show_all_file_settings", 1
		case 6250:
			name, entries = "pg_ident_file_mappings", 1
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
		case pgcatalog.AIOSOID:
			name = "pg_aios"
		case pgcatalog.BackendMemoryContextsOID:
			name = "pg_backend_memory_contexts"
		case pgcatalog.ConfigOID:
			name, entries = "pg_config", 1
		case pgcatalog.FileSettingsOID:
			name, entries = "pg_file_settings", 1
		case pgcatalog.IdentFileMappingsOID:
			name, entries = "pg_ident_file_mappings", 1
		default:
			return false
		}
		ownerMask, statsMask = authorization.TableGrantPrivileges, authorization.SelectPrivilege
	default:
		return false
	}
	owner, acl, _ := access.DescriptorACL(d)
	if owner != bootstrap || d.Name() != name || d.Namespace() != (ids.ObjectAddress{ClassID: ids.NamespaceClassID, ObjectID: ids.PGCatalogNamespaceOID}) {
		return false
	}
	if entries == 0 {
		// Shared-statistics getters have ordinary implicit PUBLIC EXECUTE.
		// Even an explicit ACL with identical effective rights is user state.
		return !acl.Explicit && len(acl.Entries) == 0
	}
	if !acl.Explicit || len(acl.Entries) != entries {
		return false
	}
	return acl.Entries[0] == (authorization.ACLEntry{Grantor: bootstrap, Grantee: authorization.ACLGrantee{Role: bootstrap}, Privileges: ownerMask}) &&
		(entries == 1 || acl.Entries[1] == (authorization.ACLEntry{Grantor: bootstrap, Grantee: authorization.ACLGrantee{Role: stats}, Privileges: statsMask}))
}
