package routine

import (
	"stuntgres/internal/engine/authorization"
	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/expr"
	"stuntgres/internal/engine/ids"
)

// Exact PostgreSQL declarations for view EXECUTE authority and catalog metadata.
// Zero provider flags deliberately leave ordinary routine execution unavailable.
var restrictedStatsDeclarations = [...]catalog.BuiltinRoutineDeclaration{
	{Identity: catalog.RoutineIdentity{ID: 5052, Namespace: ids.PGCatalogNamespaceOID, Name: "pg_get_shmem_allocations"}, Kind: catalog.FunctionRoutine, Result: 2249, Strict: true, Volatility: expr.Volatile, Parallel: 's', ReturnsSet: true, Outputs: catalog.NewRoutineOutputs([]ids.OID{25, 20, 20, 20}, "name", "off", "size", "allocated_size")},
	{Identity: catalog.RoutineIdentity{ID: 4100, Namespace: ids.PGCatalogNamespaceOID, Name: "pg_get_shmem_allocations_numa"}, Kind: catalog.FunctionRoutine, Result: 2249, Strict: true, Volatility: expr.Volatile, Parallel: 's', ReturnsSet: true, Outputs: catalog.NewRoutineOutputs([]ids.OID{25, 23, 20}, "name", "numa_node", "size")},
	{Identity: catalog.RoutineIdentity{ID: 6399, Namespace: ids.PGCatalogNamespaceOID, Name: "pg_get_aios"}, Kind: catalog.FunctionRoutine, Result: 2249, Strict: true, Volatility: expr.Volatile, Parallel: 'r', ReturnsSet: true, Outputs: catalog.NewRoutineOutputs([]ids.OID{23, 23, 20, 25, 25, 20, 20, 25, 21, 23, 25, 25, 16, 16, 16}, "pid", "io_id", "io_generation", "state", "operation", "off", "length", "target", "handle_data_len", "raw_result", "result", "target_desc", "f_sync", "f_localmem", "f_buffered")},
	{Identity: catalog.RoutineIdentity{ID: 3400, Namespace: ids.PGCatalogNamespaceOID, Name: "pg_config"}, Kind: catalog.FunctionRoutine, Result: 2249, Strict: true, Volatility: expr.Stable, Parallel: 'r', ReturnsSet: true, Outputs: catalog.NewRoutineOutputs([]ids.OID{25, 25}, "name", "setting")},
	{Identity: catalog.RoutineIdentity{ID: 3329, Namespace: ids.PGCatalogNamespaceOID, Name: "pg_show_all_file_settings"}, Kind: catalog.FunctionRoutine, Result: 2249, Strict: true, Volatility: expr.Volatile, Parallel: 's', ReturnsSet: true, Outputs: catalog.NewRoutineOutputs([]ids.OID{25, 23, 23, 25, 25, 16, 25}, "sourcefile", "sourceline", "seqno", "name", "setting", "applied", "error")},
	{Identity: catalog.RoutineIdentity{ID: 6250, Namespace: ids.PGCatalogNamespaceOID, Name: "pg_ident_file_mappings"}, Kind: catalog.FunctionRoutine, Result: 2249, Strict: true, Volatility: expr.Volatile, Parallel: 's', ReturnsSet: true, Outputs: catalog.NewRoutineOutputs([]ids.OID{23, 25, 23, 25, 25, 25, 25}, "map_number", "file_name", "line_number", "map_name", "sys_name", "pg_username", "error")},
	{Identity: catalog.RoutineIdentity{ID: 3195, Namespace: ids.PGCatalogNamespaceOID, Name: "pg_stat_get_archiver"}, Kind: catalog.FunctionRoutine, Result: 2249, Volatility: expr.Stable, Parallel: 'r', Outputs: catalog.NewRoutineOutputs([]ids.OID{20, 25, 1184, 20, 25, 1184, 1184}, "archived_count", "last_archived_wal", "last_archived_time", "failed_count", "last_failed_wal", "last_failed_time", "stats_reset")},
	{Identity: catalog.RoutineIdentity{ID: 2306, Namespace: ids.PGCatalogNamespaceOID, Name: "pg_stat_get_slru"}, Kind: catalog.FunctionRoutine, Result: 2249, Volatility: expr.Stable, Parallel: 'r', ReturnsSet: true, Outputs: catalog.NewRoutineOutputs([]ids.OID{25, 20, 20, 20, 20, 20, 20, 20, 1184}, "name", "blks_zeroed", "blks_hit", "blks_read", "blks_written", "blks_exists", "flushes", "truncates", "stats_reset")},
	{Identity: catalog.RoutineIdentity{ID: 2772, Namespace: ids.PGCatalogNamespaceOID, Name: "pg_stat_get_bgwriter_buf_written_clean"}, Kind: catalog.FunctionRoutine, Result: 20, Strict: true, Volatility: expr.Stable, Parallel: 'r'},
	{Identity: catalog.RoutineIdentity{ID: 2773, Namespace: ids.PGCatalogNamespaceOID, Name: "pg_stat_get_bgwriter_maxwritten_clean"}, Kind: catalog.FunctionRoutine, Result: 20, Strict: true, Volatility: expr.Stable, Parallel: 'r'},
	{Identity: catalog.RoutineIdentity{ID: 2859, Namespace: ids.PGCatalogNamespaceOID, Name: "pg_stat_get_buf_alloc"}, Kind: catalog.FunctionRoutine, Result: 20, Strict: true, Volatility: expr.Stable, Parallel: 'r'},
	{Identity: catalog.RoutineIdentity{ID: 3075, Namespace: ids.PGCatalogNamespaceOID, Name: "pg_stat_get_bgwriter_stat_reset_time"}, Kind: catalog.FunctionRoutine, Result: 1184, Strict: true, Volatility: expr.Stable, Parallel: 'r'},
	{Identity: catalog.RoutineIdentity{ID: 6118, Namespace: ids.PGCatalogNamespaceOID, Name: "pg_stat_get_subscription", Signature: "26"}, Kind: catalog.FunctionRoutine, Result: 2249, Volatility: expr.Stable, Parallel: 'r', ReturnsSet: true, Outputs: catalog.NewRoutineOutputs([]ids.OID{26, 26, 23, 23, 3220, 1184, 1184, 3220, 1184, 25}, "subid", "subid", "relid", "pid", "leader_pid", "received_lsn", "last_msg_send_time", "last_msg_receipt_time", "latest_end_lsn", "latest_end_time", "worker_type")},
	{Identity: catalog.RoutineIdentity{ID: 6014, Namespace: ids.PGCatalogNamespaceOID, Name: "pg_show_replication_origin_status"}, Kind: catalog.FunctionRoutine, Result: 2249, Volatility: expr.Volatile, Parallel: 'r', ReturnsSet: true, Outputs: catalog.NewRoutineOutputs([]ids.OID{26, 25, 3220, 3220}, "local_id", "external_id", "remote_lsn", "local_lsn")},
	{Identity: catalog.RoutineIdentity{ID: 2769, Namespace: ids.PGCatalogNamespaceOID, Name: "pg_stat_get_checkpointer_num_timed"}, Kind: catalog.FunctionRoutine, Result: 20, Strict: true, Volatility: expr.Stable, Parallel: 'r'},
	{Identity: catalog.RoutineIdentity{ID: 2770, Namespace: ids.PGCatalogNamespaceOID, Name: "pg_stat_get_checkpointer_num_requested"}, Kind: catalog.FunctionRoutine, Result: 20, Strict: true, Volatility: expr.Stable, Parallel: 'r'},
	{Identity: catalog.RoutineIdentity{ID: 6377, Namespace: ids.PGCatalogNamespaceOID, Name: "pg_stat_get_checkpointer_num_performed"}, Kind: catalog.FunctionRoutine, Result: 20, Strict: true, Volatility: expr.Stable, Parallel: 'r'},
	{Identity: catalog.RoutineIdentity{ID: 6327, Namespace: ids.PGCatalogNamespaceOID, Name: "pg_stat_get_checkpointer_restartpoints_timed"}, Kind: catalog.FunctionRoutine, Result: 20, Strict: true, Volatility: expr.Stable, Parallel: 'r'},
	{Identity: catalog.RoutineIdentity{ID: 6328, Namespace: ids.PGCatalogNamespaceOID, Name: "pg_stat_get_checkpointer_restartpoints_requested"}, Kind: catalog.FunctionRoutine, Result: 20, Strict: true, Volatility: expr.Stable, Parallel: 'r'},
	{Identity: catalog.RoutineIdentity{ID: 6329, Namespace: ids.PGCatalogNamespaceOID, Name: "pg_stat_get_checkpointer_restartpoints_performed"}, Kind: catalog.FunctionRoutine, Result: 20, Strict: true, Volatility: expr.Stable, Parallel: 'r'},
	{Identity: catalog.RoutineIdentity{ID: 3160, Namespace: ids.PGCatalogNamespaceOID, Name: "pg_stat_get_checkpointer_write_time"}, Kind: catalog.FunctionRoutine, Result: 701, Strict: true, Volatility: expr.Stable, Parallel: 'r'},
	{Identity: catalog.RoutineIdentity{ID: 3161, Namespace: ids.PGCatalogNamespaceOID, Name: "pg_stat_get_checkpointer_sync_time"}, Kind: catalog.FunctionRoutine, Result: 701, Strict: true, Volatility: expr.Stable, Parallel: 'r'},
	{Identity: catalog.RoutineIdentity{ID: 2771, Namespace: ids.PGCatalogNamespaceOID, Name: "pg_stat_get_checkpointer_buffers_written"}, Kind: catalog.FunctionRoutine, Result: 20, Strict: true, Volatility: expr.Stable, Parallel: 'r'},
	{Identity: catalog.RoutineIdentity{ID: 6366, Namespace: ids.PGCatalogNamespaceOID, Name: "pg_stat_get_checkpointer_slru_written"}, Kind: catalog.FunctionRoutine, Result: 20, Strict: true, Volatility: expr.Stable, Parallel: 'r'},
	{Identity: catalog.RoutineIdentity{ID: 6314, Namespace: ids.PGCatalogNamespaceOID, Name: "pg_stat_get_checkpointer_stat_reset_time"}, Kind: catalog.FunctionRoutine, Result: 1184, Strict: true, Volatility: expr.Stable, Parallel: 'r'},
	{Identity: catalog.RoutineIdentity{ID: 3046, Namespace: ids.PGCatalogNamespaceOID, Name: "pg_stat_get_xact_function_calls", Signature: "26"}, Kind: catalog.FunctionRoutine, Result: 20, Strict: true, Volatility: expr.Volatile, Parallel: 'r'},
	{Identity: catalog.RoutineIdentity{ID: 3047, Namespace: ids.PGCatalogNamespaceOID, Name: "pg_stat_get_xact_function_total_time", Signature: "26"}, Kind: catalog.FunctionRoutine, Result: 701, Strict: true, Volatility: expr.Volatile, Parallel: 'r'},
	{Identity: catalog.RoutineIdentity{ID: 3048, Namespace: ids.PGCatalogNamespaceOID, Name: "pg_stat_get_xact_function_self_time", Signature: "26"}, Kind: catalog.FunctionRoutine, Result: 701, Strict: true, Volatility: expr.Volatile, Parallel: 'r'},
	{Identity: catalog.RoutineIdentity{ID: 2978, Namespace: ids.PGCatalogNamespaceOID, Name: "pg_stat_get_function_calls", Signature: "26"}, Kind: catalog.FunctionRoutine, Result: 20, Strict: true, Volatility: expr.Stable, Parallel: 'r'},
	{Identity: catalog.RoutineIdentity{ID: 2979, Namespace: ids.PGCatalogNamespaceOID, Name: "pg_stat_get_function_total_time", Signature: "26"}, Kind: catalog.FunctionRoutine, Result: 701, Strict: true, Volatility: expr.Stable, Parallel: 'r'},
	{Identity: catalog.RoutineIdentity{ID: 2980, Namespace: ids.PGCatalogNamespaceOID, Name: "pg_stat_get_function_self_time", Signature: "26"}, Kind: catalog.FunctionRoutine, Result: 701, Strict: true, Volatility: expr.Stable, Parallel: 'r'},
}

// RestrictedStatsProcedures creates engine-owned ACL descriptors, without adding
// the declarations to a callable provider or scalar-selector inventory.
// pg_config, pg_show_all_file_settings, pg_ident_file_mappings and origin status retain owner-only EXECUTE.
// Archiver, SLRU, bgwriter, checkpointer and subscription retain PUBLIC EXECUTE.
func RestrictedStatsProcedures(owner, stats authorization.PrincipalRef) []catalog.Procedure {
	procedures := make([]catalog.Procedure, len(restrictedStatsDeclarations))
	for i := range restrictedStatsDeclarations {
		declaration := &restrictedStatsDeclarations[i]
		var acl authorization.ACLValue
		if id := declaration.Identity.ID; !publicStatsGetter(id) {
			count := 2
			if declaration.Identity.ID == 3400 || declaration.Identity.ID == 3329 || declaration.Identity.ID == 6250 || RestrictedReplicationOriginRoutine(declaration.Identity.ID) {
				count = 1
			}
			entries := make([]authorization.ACLEntry, count)
			entries[0] = authorization.ACLEntry{Grantor: owner, Grantee: authorization.ACLGrantee{Role: owner}, Privileges: authorization.ExecutePrivilege}
			if count == 2 {
				entries[1] = authorization.ACLEntry{Grantor: owner, Grantee: authorization.ACLGrantee{Role: stats}, Privileges: authorization.ExecutePrivilege}
			}
			acl = authorization.ACLValue{Explicit: true, Entries: entries}
		}
		procedures[i] = catalog.Procedure{
			ObjectAddress:   ids.ObjectAddress{ClassID: ids.ProcedureClassID, ObjectID: declaration.Identity.ID},
			ObjectNamespace: ids.ObjectAddress{ClassID: ids.NamespaceClassID, ObjectID: ids.PGCatalogNamespaceOID},
			ObjectRevision:  1,
			ObjectName:      declaration.Identity.Name,
			Signature:       declaration.Identity.Signature,
			RoutineKind:     declaration.Kind,
			Builtin:         declaration,
			Owner:           owner,
			Result:          declaration.Result,
			Volatility:      declaration.Volatility,
			Strict:          declaration.Strict,
			Display:         catalog.RoutineDisplay{Language: catalog.InternalRoutineLanguage, Source: declaration.Identity.Name},
			ACL:             acl,
		}
		if declaration.Identity.ID == 3329 {
			procedures[i].Display.Source = "show_all_file_settings"
		}
	}
	return procedures
}

func publicStatsGetter(id ids.OID) bool {
	switch id {
	case 3195, 2306, 2772, 2773, 2859, 3075, 6118,
		2769, 2770, 6377, 6327, 6328, 6329, 3160, 3161, 2771, 6366, 6314, 3046, 3047, 3048, 2978, 2979, 2980:
		return true
	}
	return false
}

// SeedBackendMemoryACL modifies the canonical callable descriptor, never adds
// a second metadata-only declaration. Subsequent admission uses ordinary ACLs.
func SeedBackendMemoryACL(procedures []catalog.Procedure, owner, stats authorization.PrincipalRef) {
	for i := range procedures {
		p := &procedures[i]
		if p.Address().ObjectID != 2282 {
			continue
		}
		p.ACL = authorization.ACLValue{Explicit: true, Entries: []authorization.ACLEntry{
			{Grantor: owner, Grantee: authorization.ACLGrantee{Role: owner}, Privileges: authorization.ExecutePrivilege},
			{Grantor: owner, Grantee: authorization.ACLGrantee{Role: stats}, Privileges: authorization.ExecutePrivilege},
		}}
	}
}
