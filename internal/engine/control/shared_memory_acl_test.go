package control

import (
	"slices"
	"testing"

	"stuntgres/internal/engine/authorization"
	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/engine/routine"
)

func TestPGShmemACLReplayBaseline(t *testing.T) {
	domain := new(authorization.Domain)
	root := authorization.Seed(domain)
	roles := root.Read(domain.Principal(10))
	owner, stats := domain.Principal(10), roles.LookupRef("pg_read_all_stats")
	var descriptors []catalog.Descriptor
	procedures := routine.SharedMemoryProcedures(owner, stats)
	for i := range procedures { descriptors = append(descriptors, &procedures[i]) }
	for i, name := range []string{"pg_shmem_allocations", "pg_shmem_allocations_numa"} {
		oid := ids.OID(12134)
		if i == 1 { oid = 12138 }
		descriptors = append(descriptors, &catalog.Relation{ObjectAddress:ids.ObjectAddress{ClassID:ids.RelationClassID,ObjectID:oid}, ObjectNamespace:ids.ObjectAddress{ClassID:ids.NamespaceClassID,ObjectID:11}, ObjectName:name, Owner:owner, RelationKind:catalog.CatalogRelation, VirtualView:true, Security:catalog.RelationSecurity{ACL:authorization.ACLValue{Explicit:true, Entries:[]authorization.ACLEntry{
			{Grantor:owner, Grantee:authorization.ACLGrantee{Role:owner}, Privileges:authorization.TableGrantPrivileges},
			{Grantor:owner, Grantee:authorization.ACLGrantee{Role:stats}, Privileges:authorization.SelectPrivilege},
		}}}})
	}
	for _, descriptor := range descriptors {
		t.Run(descriptor.Name(), func(t *testing.T) {
			if !sharedMemoryACLReconstructed(descriptor, owner, stats) { t.Fatal("seeded authority needs no replay") }
			for _, change := range []func(*authorization.ACLValue){
				func(a *authorization.ACLValue){a.Explicit=false},
				func(a *authorization.ACLValue){a.Entries=nil},
				func(a *authorization.ACLValue){a.Entries[0],a.Entries[1]=a.Entries[1],a.Entries[0]},
				func(a *authorization.ACLValue){a.Entries=append(a.Entries,a.Entries[0])},
				func(a *authorization.ACLValue){a.Entries[1].GrantOptions=a.Entries[1].Privileges},
				func(a *authorization.ACLValue){a.Entries[1].Grantor=stats},
				func(a *authorization.ACLValue){a.Entries[1].Grantee.Role=new(authorization.Domain).Principal(stats.ID())},
			} {
				var modified catalog.Descriptor
				switch original := descriptor.(type) {
				case *catalog.Procedure:
					x := *original; x.ACL.Entries=slices.Clone(x.ACL.Entries); change(&x.ACL); modified=&x
				case *catalog.Relation:
					x := *original; x.Security.ACL.Entries=slices.Clone(x.Security.ACL.Entries); change(&x.Security.ACL); modified=&x
				}
				if sharedMemoryACLReconstructed(modified,owner,stats) { t.Fatal("changed raw ACL was discarded", modified) }
			}
			if sharedMemoryACLReconstructed(descriptor,stats,owner) { t.Fatal("foreign baseline owner accepted") }
			if original, ok := descriptor.(*catalog.Relation); ok {
				x := *original
				x.Security.ColumnACL = map[int32]authorization.ACLValue{1:{Explicit:true, Entries:[]authorization.ACLEntry{{Grantor:owner, Grantee:authorization.ACLGrantee{Role:stats}, Privileges:authorization.SelectPrivilege}}}}
				if sharedMemoryACLReconstructed(&x,owner,stats) { t.Fatal("column ACL was discarded") }
			}
		})
	}
}
