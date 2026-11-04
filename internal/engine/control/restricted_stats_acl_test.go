package control

import (
	"slices"
	"testing"

	"stuntgres/internal/engine/access"
	"stuntgres/internal/engine/authorization"
	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/engine/routine"
)

func TestRestrictedStatsACLReplayBaseline(t *testing.T) {
	domain := new(authorization.Domain)
	root := authorization.Seed(domain)
	roles := root.Read(domain.Principal(10))
	owner, stats := domain.Principal(10), roles.LookupRef("pg_read_all_stats")
	var descriptors []catalog.Descriptor
	procedures := routine.RestrictedStatsProcedures(owner, stats)
	callable := routine.BuiltinProcedures(owner)
	routine.SeedBackendMemoryACL(callable, owner, stats)
	for i := range callable {
		if callable[i].Address().ObjectID == 2282 {
			descriptors = append(descriptors, &callable[i])
		}
	}
	for i := range procedures {
		// Origin backing ACLs belong to the restricted-origin reconstruction
		// owner and its independent mutation matrix, not the stats seed owner.
		if !routine.RestrictedReplicationOriginRoutine(procedures[i].Address().ObjectID) {
			descriptors = append(descriptors, &procedures[i])
		}
	}
	for i, name := range []string{"pg_shmem_allocations", "pg_shmem_allocations_numa", "pg_aios", "pg_backend_memory_contexts"} {
		oid := ids.OID(12134)
		if i == 1 {
			oid = 12138
		}
		if i == 2 {
			oid = 12355
		}
		if i == 3 {
			oid = 12142
		}
		descriptors = append(descriptors, &catalog.Relation{ObjectAddress: ids.ObjectAddress{ClassID: ids.RelationClassID, ObjectID: oid}, ObjectNamespace: ids.ObjectAddress{ClassID: ids.NamespaceClassID, ObjectID: 11}, ObjectName: name, Owner: owner, RelationKind: catalog.CatalogRelation, VirtualView: true, Security: catalog.RelationSecurity{ACL: authorization.ACLValue{Explicit: true, Entries: []authorization.ACLEntry{
			{Grantor: owner, Grantee: authorization.ACLGrantee{Role: owner}, Privileges: authorization.TableGrantPrivileges},
			{Grantor: owner, Grantee: authorization.ACLGrantee{Role: stats}, Privileges: authorization.SelectPrivilege},
		}}}})
	}
	descriptors = append(descriptors, &catalog.Relation{
		ObjectAddress:   ids.ObjectAddress{ClassID: ids.RelationClassID, ObjectID: 12130},
		ObjectNamespace: ids.ObjectAddress{ClassID: ids.NamespaceClassID, ObjectID: 11},
		ObjectName:      "pg_config", Owner: owner, RelationKind: catalog.CatalogRelation, VirtualView: true,
		Security: catalog.RelationSecurity{ACL: authorization.ACLValue{Explicit: true, Entries: []authorization.ACLEntry{
			{Grantor: owner, Grantee: authorization.ACLGrantee{Role: owner}, Privileges: authorization.TableGrantPrivileges},
		}}},
	})
	descriptors = append(descriptors, &catalog.Relation{
		ObjectAddress:   ids.ObjectAddress{ClassID: ids.RelationClassID, ObjectID: 12110},
		ObjectNamespace: ids.ObjectAddress{ClassID: ids.NamespaceClassID, ObjectID: 11},
		ObjectName:      "pg_file_settings", Owner: owner, RelationKind: catalog.CatalogRelation, VirtualView: true,
		Security: catalog.RelationSecurity{ACL: authorization.ACLValue{Explicit: true, Entries: []authorization.ACLEntry{
			{Grantor: owner, Grantee: authorization.ACLGrantee{Role: owner}, Privileges: authorization.TableGrantPrivileges},
		}}},
	})
	descriptors = append(descriptors, &catalog.Relation{
		ObjectAddress:   ids.ObjectAddress{ClassID: ids.RelationClassID, ObjectID: 12118},
		ObjectNamespace: ids.ObjectAddress{ClassID: ids.NamespaceClassID, ObjectID: 11},
		ObjectName:      "pg_ident_file_mappings", Owner: owner, RelationKind: catalog.CatalogRelation, VirtualView: true,
		Security: catalog.RelationSecurity{ACL: authorization.ACLValue{Explicit: true, Entries: []authorization.ACLEntry{
			{Grantor: owner, Grantee: authorization.ACLGrantee{Role: owner}, Privileges: authorization.TableGrantPrivileges},
		}}},
	})
	descriptors = append(descriptors, &catalog.Relation{
		ObjectAddress:   ids.ObjectAddress{ClassID: ids.RelationClassID, ObjectID: 12343},
		ObjectNamespace: ids.ObjectAddress{ClassID: ids.NamespaceClassID, ObjectID: 11},
		ObjectName:      "pg_replication_origin_status", Owner: owner, RelationKind: catalog.CatalogRelation, VirtualView: true,
		Security: catalog.RelationSecurity{ACL: authorization.ACLValue{Explicit: true, Entries: []authorization.ACLEntry{
			{Grantor: owner, Grantee: authorization.ACLGrantee{Role: owner}, Privileges: authorization.TableGrantPrivileges},
		}}},
	})
	for _, descriptor := range descriptors {
		t.Run(descriptor.Name(), func(t *testing.T) {
			if !restrictedStatsACLReconstructed(descriptor, owner, stats) {
				t.Fatal("seeded authority needs no replay")
			}
			_, baseline, _ := access.DescriptorACL(descriptor)
			oid := descriptor.Address().ObjectID
			implicit := descriptor.Address().ClassID == ids.ProcedureClassID && (oid == 3195 || oid == 2306 || oid == 2772 || oid == 2773 || oid == 2859 || oid == 3075 || oid == 6118 || oid == 2769 || oid == 2770 || oid == 6377 || oid == 6327 || oid == 6328 || oid == 6329 || oid == 3160 || oid == 3161 || oid == 2771 || oid == 6366 || oid == 6314 || oid == 3046 || oid == 3047 || oid == 3048 || oid == 2978 || oid == 2979 || oid == 2980)
			if implicit != !baseline.Explicit || implicit && len(baseline.Entries) != 0 {
				t.Fatal("backing routine default ACL classification", descriptor)
			}
			changes := []func(*authorization.ACLValue){
				func(a *authorization.ACLValue) { a.Explicit = false },
				func(a *authorization.ACLValue) { a.Entries = nil },
				func(a *authorization.ACLValue) {
					if len(a.Entries) == 1 {
						a.Entries[0].Grantee.Public = true
					} else {
						a.Entries[0], a.Entries[1] = a.Entries[1], a.Entries[0]
					}
				},
				func(a *authorization.ACLValue) { a.Entries = append(a.Entries, a.Entries[0]) },
				func(a *authorization.ACLValue) {
					a.Entries[len(a.Entries)-1].GrantOptions = a.Entries[len(a.Entries)-1].Privileges
				},
				func(a *authorization.ACLValue) { a.Entries[len(a.Entries)-1].Grantor = stats },
				func(a *authorization.ACLValue) {
					a.Entries[len(a.Entries)-1].Grantee.Role = new(authorization.Domain).Principal(stats.ID())
				},
				func(a *authorization.ACLValue) { a.Entries[len(a.Entries)-1].Privileges = 0 },
			}
			if implicit {
				changes = []func(*authorization.ACLValue){
					func(a *authorization.ACLValue) { a.Explicit = true },
					func(a *authorization.ACLValue) {
						*a = authorization.ACLValue{Explicit: true, Entries: []authorization.ACLEntry{
							{Grantor: owner, Grantee: authorization.ACLGrantee{Public: true}, Privileges: authorization.ExecutePrivilege},
							{Grantor: owner, Grantee: authorization.ACLGrantee{Role: owner}, Privileges: authorization.ExecutePrivilege},
						}}
					},
					func(a *authorization.ACLValue) {
						a.Entries = []authorization.ACLEntry{{Grantor: owner, Grantee: authorization.ACLGrantee{Role: stats}, Privileges: authorization.ExecutePrivilege}}
					},
				}
				public := authorization.ACLEntry{Grantor: owner, Grantee: authorization.ACLGrantee{Public: true}, Privileges: authorization.ExecutePrivilege}
				ownerEntry := authorization.ACLEntry{Grantor: owner, Grantee: authorization.ACLGrantee{Role: owner}, Privileges: authorization.ExecutePrivilege}
				statsEntry := authorization.ACLEntry{Grantor: owner, Grantee: authorization.ACLGrantee{Role: stats}, Privileges: authorization.ExecutePrivilege}
				withOption := statsEntry
				withOption.GrantOptions = authorization.ExecutePrivilege
				otherGrantor := statsEntry
				otherGrantor.Grantor = stats
				foreignPrincipal := statsEntry
				foreignPrincipal.Grantee.Role = new(authorization.Domain).Principal(stats.ID())
				for _, entries := range [][]authorization.ACLEntry{
					{ownerEntry, public},
					{public, ownerEntry, public},
					{ownerEntry, withOption},
					{statsEntry, ownerEntry, withOption},
					{ownerEntry, otherGrantor},
					{ownerEntry, foreignPrincipal},
				} {
					changes = append(changes, func(a *authorization.ACLValue) {
						*a = authorization.ACLValue{Explicit: true, Entries: slices.Clone(entries)}
					})
				}
			}
			for _, change := range changes {
				var modified catalog.Descriptor
				switch original := descriptor.(type) {
				case *catalog.Procedure:
					x := *original
					x.ACL.Entries = slices.Clone(x.ACL.Entries)
					change(&x.ACL)
					modified = &x
				case *catalog.Relation:
					x := *original
					x.Security.ACL.Entries = slices.Clone(x.Security.ACL.Entries)
					change(&x.Security.ACL)
					modified = &x
				}
				if restrictedStatsACLReconstructed(modified, owner, stats) {
					t.Fatal("changed raw ACL was discarded", modified)
				}
			}
			if restrictedStatsACLReconstructed(descriptor, stats, owner) {
				t.Fatal("foreign baseline owner accepted")
			}
			switch original := descriptor.(type) {
			case *catalog.Procedure:
				for _, change := range []func(*catalog.Procedure){
					func(x *catalog.Procedure) { x.Owner = stats },
					func(x *catalog.Procedure) { x.ObjectAddress.ClassID = ids.RelationClassID },
					func(x *catalog.Procedure) { x.ObjectAddress.SubID = 1 },
					func(x *catalog.Procedure) { x.ObjectAddress.ObjectID++ },
					func(x *catalog.Procedure) { x.ObjectName = "changed" },
					func(x *catalog.Procedure) { x.ObjectNamespace.ObjectID++ },
					func(x *catalog.Procedure) { x.Signature = "25" },
					func(x *catalog.Procedure) { copyDeclaration := *x.Builtin; x.Builtin = &copyDeclaration },
				} {
					x := *original
					change(&x)
					if restrictedStatsACLReconstructed(&x, owner, stats) {
						t.Fatal("changed procedure identity discarded")
					}
				}
			case *catalog.Relation:
				for _, change := range []func(*catalog.Relation){
					func(x *catalog.Relation) { x.Owner = stats },
					func(x *catalog.Relation) { x.ObjectName = "changed" },
					func(x *catalog.Relation) { x.ObjectNamespace.ObjectID++ },
				} {
					x := *original
					change(&x)
					if restrictedStatsACLReconstructed(&x, owner, stats) {
						t.Fatal("changed relation identity discarded")
					}
				}
			}
			if original, ok := descriptor.(*catalog.Relation); ok {
				x := *original
				x.Security.ColumnACL = map[int32]authorization.ACLValue{1: {Explicit: true, Entries: []authorization.ACLEntry{{Grantor: owner, Grantee: authorization.ACLGrantee{Role: stats}, Privileges: authorization.SelectPrivilege}}}}
				if restrictedStatsACLReconstructed(&x, owner, stats) {
					t.Fatal("column ACL was discarded")
				}
			}
		})
	}
}
