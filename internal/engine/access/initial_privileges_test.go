package access

import (
	"testing"

	"stuntgres/internal/engine/authorization"
	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/ids"
)

func TestPGInitPrivsRoleDependencies(t *testing.T) {
	f := newAccessFixture(t)
	old := f.owner.Principals.Principal(100)
	reader := f.owner.Principals.Principal(101)
	address := f.public.Address()
	column := ids.ObjectAddress{ClassID: ids.RelationClassID, ObjectID: 30001, SubID: 3}
	acl := authorization.ACLValue{Explicit: true, Entries: []authorization.ACLEntry{
		{Grantor: old, Grantee: authorization.ACLGrantee{Role: old}, Privileges: authorization.CreatePrivilege},
		{Grantor: old, Grantee: authorization.ACLGrantee{Role: reader}, Privileges: authorization.UsagePrivilege},
		{Grantor: reader, Grantee: authorization.ACLGrantee{Public: true}, Privileges: authorization.UsagePrivilege},
	}}
	f.read.State().SetInitialPrivileges(map[ids.ObjectAddress]*catalog.InitialPrivilegeRecord{
		address: {PrivType: 'e', ACL: acl}, column: {PrivType: 'e', ACL: acl},
	})
	for _, ref := range []authorization.PrincipalRef{old, reader} {
		var deps []RoleDependency
		if !InitialRoleDependencies(f.read, ref, nil) {
			t.Fatal("historical reference did not stop retirement", ref)
		}
		InitialRoleDependencies(f.read, ref, &deps)
		if len(deps) != 2 {
			t.Fatal("dependency was duplicated per entry or omitted", deps)
		}
		seen := map[ids.ObjectAddress]bool{}
		for _, dep := range deps {
			if dep.Kind != 'i' || seen[dep.Address] {
				t.Fatal("wrong dependency kind or duplicate address", deps)
			}
			seen[dep.Address] = true
		}
		if !seen[address] || !seen[column] {
			t.Fatal("full column/object identity lost", deps)
		}
	}
	if InitialRoleDependencies(f.read, f.bootstrap, nil) {
		t.Fatal("current owner was substituted for historical principals")
	}
	if InitialRoleDependencies(f.read, authorization.PrincipalRef{}, nil) {
		t.Fatal("PUBLIC was treated as a principal")
	}
}
