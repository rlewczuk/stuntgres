package core

import (
	"slices"

	"stuntgres/internal/engine/authorization"
	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/engine/routine"
)

// initializeInitialPrivileges captures released initdb grants once. In
// particular catalog relations' simplified live ACLs are not the seed facts.
// Called only while a new engine is private, including Reset's fresh engine.
func (e *Engine) initializeInitialPrivileges() error {
	state := e.Catalog.State
	roles := e.RoleStore.PublishedRead(&e.Catalog.Owner, e.Catalog.Current())
	records := make(map[ids.ObjectAddress]*catalog.InitialPrivilegeRecord)
	capture := func(address ids.ObjectAddress, acl authorization.ACLValue) {
		if !acl.Explicit || len(acl.Entries) == 0 {
			return
		}
		acl.Entries = slices.Clone(acl.Entries)
		records[address] = &catalog.InitialPrivilegeRecord{PrivType: 'i', ACL: acl}
	}
	// initdb snapshots privileges before setup_schema creates information_schema.
	// Its later explicit current grants are not bootstrap historical records.
	informationSchema, _ := state.LookupName(ids.NameKey{ClassID: ids.NamespaceClassID, Name: "information_schema"})
	visit := func(address ids.ObjectAddress, descriptor catalog.Descriptor) {
		if descriptor == nil || address == informationSchema || descriptor.Namespace() == informationSchema {
			return
		}
		switch d := descriptor.(type) {
		case *catalog.Namespace:
			capture(address, d.ACL)
		case *catalog.Procedure:
			// Restricted builtin declarations encode the released REVOKE/GRANTs.
			if routine.RestrictedStatsResetRoutine(address.ObjectID) || routine.GenericFileRoutine(address.ObjectID) || routine.RestrictedReplicationOriginRoutine(address.ObjectID) || routine.RestrictedRecoveryControlRoutine(address.ObjectID) || routine.RestrictedServerSignalRoutine(address.ObjectID) || routine.RestrictedWALPositionRoutine(address.ObjectID) || address.ObjectID == 2282 || address.ObjectID == 5052 || address.ObjectID == 4100 || address.ObjectID == 6399 || address.ObjectID == 3400 || address.ObjectID == 3329 || address.ObjectID == 6250 || address.ObjectID == 3401 {
				capture(address, d.ACL)
			}
		case *catalog.Relation:
			if d.RelationKind != catalog.CatalogRelation {
				return
			}
			acl := authorization.ACLValue{Explicit: true, Entries: []authorization.ACLEntry{
				{Grantor: d.Owner, Grantee: authorization.ACLGrantee{Role: d.Owner}, Privileges: authorization.TableGrantPrivileges},
			}}
			public := authorization.SelectPrivilege
			switch d.Name() {
			case "pg_authid", "pg_shadow", "pg_statistic_ext_data", "pg_largeobject", "pg_subscription", "pg_replication_origin_status", "pg_user_mapping", "pg_config", "pg_file_settings", "pg_ident_file_mappings", "pg_hba_file_rules":
				public = 0
			case "pg_settings":
				public |= authorization.UpdatePrivilege
			case "pg_shmem_allocations", "pg_shmem_allocations_numa", "pg_aios", "pg_backend_memory_contexts":
				public = 0
				acl.Entries = append(acl.Entries, authorization.ACLEntry{Grantor: d.Owner, Grantee: authorization.ACLGrantee{Role: roles.LookupRef("pg_read_all_stats")}, Privileges: authorization.SelectPrivilege})
			}
			if public != 0 {
				acl.Entries = append(acl.Entries, authorization.ACLEntry{Grantor: d.Owner, Grantee: authorization.ACLGrantee{Public: true}, Privileges: public})
			}
			capture(address, acl)
			if d.Name() == "pg_subscription" {
				for sub, acl := range d.Security.ColumnACL {
					column := address
					column.SubID = sub
					capture(column, acl)
				}
			}
		}
	}
	// The seed's privilege-bearing objects are its namespaces, the restricted
	// routines and its catalog relations; visit only those.
	for address := range catalog.BuiltinNamespaces() {
		visit(address, state.Get(address))
	}
	for oid := range routine.GenericFileRoutines() {
		address := ids.ObjectAddress{ClassID: ids.ProcedureClassID, ObjectID: oid}
		visit(address, state.Get(address))
	}
	for oid := range routine.RestrictedRecoveryControlRoutines() {
		address := ids.ObjectAddress{ClassID: ids.ProcedureClassID, ObjectID: oid}
		visit(address, state.Get(address))
	}
	for oid := range routine.RestrictedStatsResetRoutines() {
		address := ids.ObjectAddress{ClassID: ids.ProcedureClassID, ObjectID: oid}
		visit(address, state.Get(address))
	}
	for oid := range routine.RestrictedServerSignalRoutines() {
		address := ids.ObjectAddress{ClassID: ids.ProcedureClassID, ObjectID: oid}
		visit(address, state.Get(address))
	}
	for oid := range routine.RestrictedWALPositionRoutines() {
		address := ids.ObjectAddress{ClassID: ids.ProcedureClassID, ObjectID: oid}
		visit(address, state.Get(address))
	}
	for _, oid := range [...]ids.OID{6003, 6004, 6005, 6006, 6007, 6008, 6009, 6010, 6011, 6012, 6013, 6014, 2282, 5052, 4100, 6399, 3400, 3329, 6250, 3401} {
		address := ids.ObjectAddress{ClassID: ids.ProcedureClassID, ObjectID: oid}
		visit(address, state.Get(address))
	}
	for address, relation := range state.Relations() {
		visit(address, relation)
	}
	state.SetInitialPrivileges(records)
	if err := catalog.ValidateInitialPrivileges(roles, e.Catalog.Current()); err != nil {
		return err
	}
	state.SetInitialPrivilegeBaseline(records)
	return nil
}
