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
	for address, descriptor := range state.Descriptors() {
		if address == informationSchema || descriptor.Namespace() == informationSchema {
			continue
		}
		switch d := descriptor.(type) {
		case *catalog.Namespace:
			capture(address, d.ACL)
		case *catalog.Procedure:
			// Restricted builtin declarations encode the released REVOKE/GRANTs.
			if routine.GenericFileRoutine(address.ObjectID) || address.ObjectID == 5052 || address.ObjectID == 4100 {
				capture(address, d.ACL)
			}
		case *catalog.Relation:
			if d.RelationKind != catalog.CatalogRelation {
				continue
			}
			acl := authorization.ACLValue{Explicit: true, Entries: []authorization.ACLEntry{
				{Grantor: d.Owner, Grantee: authorization.ACLGrantee{Role: d.Owner}, Privileges: authorization.TableGrantPrivileges},
			}}
			public := authorization.SelectPrivilege
			switch d.Name() {
			case "pg_authid", "pg_shadow", "pg_statistic_ext_data", "pg_largeobject", "pg_subscription", "pg_user_mapping":
				public = 0
			case "pg_settings":
				public |= authorization.UpdatePrivilege
			case "pg_shmem_allocations", "pg_shmem_allocations_numa":
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
	state.SetInitialPrivileges(records)
	if err := catalog.ValidateInitialPrivileges(roles, e.Catalog.Current()); err != nil {
		return err
	}
	state.SetInitialPrivilegeBaseline(records)
	return nil
}
