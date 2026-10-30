package core_test

import (
	"testing"

	"stuntgres/internal/engine"
	"stuntgres/internal/engine/authorization"
	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/ids"
)

func TestReplicationOriginStateInitialPrivileges(t *testing.T) {
	e := engine.New()
	read := e.CatalogRead()
	for _, oid := range [...]ids.OID{6003, 6004, 6005, 6012, 6013} {
		address := ids.ObjectAddress{ClassID: ids.ProcedureClassID, ObjectID: oid}
		procedure, ok := read.Get(address).(*catalog.Procedure)
		if !ok {
			t.Fatal("missing released origin declaration", address)
		}
		initial := read.State().InitialPrivilege(address)
		if initial == nil || initial.PrivType != 'i' || !initial.ACL.Explicit || len(initial.ACL.Entries) != 1 {
			t.Fatal("missing restricted initdb history", address, initial)
		}
		want := authorization.ACLEntry{Grantor: procedure.Owner, Grantee: authorization.ACLGrantee{Role: procedure.Owner}, Privileges: authorization.ExecutePrivilege}
		if initial.ACL.Entries[0] != want || !procedure.ACL.Explicit || len(procedure.ACL.Entries) != 1 || procedure.ACL.Entries[0] != want {
			t.Fatal("origin live/history ACL differs from exact owner-only seed", address, initial.ACL, procedure.ACL)
		}
		if &initial.ACL.Entries[0] == &procedure.ACL.Entries[0] {
			t.Fatal("initial privilege history borrows live ACL backing", address)
		}
		// A later live ACL replacement cannot rewrite the retained history.
		next := catalog.CloneState(read.State())
		replacement := *procedure
		replacement.ACL = authorization.ACLValue{}
		next.SetDescriptor(address, &replacement)
		if next.InitialPrivilege(address) != initial || initial.ACL.Entries[0] != want || read.Get(address) != procedure || procedure.ACL.Entries[0] != want {
			t.Fatal("live ACL replacement changed immutable initial history", address)
		}
	}
}
