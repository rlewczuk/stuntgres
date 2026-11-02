package extension_test

import (
	"reflect"
	"testing"

	"stuntgres/internal/engine"
	"stuntgres/internal/engine/authorization"
	"stuntgres/internal/engine/enginetest"
	"stuntgres/internal/engine/extension"
	"stuntgres/internal/engine/ids"
)

func TestPgsurgeryFreezeProviderCreationACL(t *testing.T) {
	e := engine.New()
	for _, q := range []string{
		"CREATE ROLE surgery_global", "CREATE ROLE surgery_scoped", "CREATE SCHEMA surgery_acl",
		"ALTER DEFAULT PRIVILEGES GRANT EXECUTE ON FUNCTIONS TO surgery_global WITH GRANT OPTION",
		"ALTER DEFAULT PRIVILEGES IN SCHEMA surgery_acl GRANT EXECUTE ON FUNCTIONS TO surgery_scoped",
	} {
		enginetest.Run(t, e, q)
	}
	op, done := enginetest.RoleOperation(t, e, "postgres")
	defer done()
	enginetest.RoleExecute(t, e, op, "CREATE EXTENSION pg_surgery SCHEMA surgery_acl")
	view := e.CatalogRead()
	ext := view.MustExtension(view.NameAddress(ids.NameKey{ClassID: ids.ExtensionClassID, Name: "pg_surgery"}))
	proc := view.MustProcedure(ext.Members[0])
	global, _ := e.Roles().Lookup("surgery_global")
	scoped, _ := e.Roles().Lookup("surgery_scoped")
	want := authorization.ACLValue{Explicit: true, Entries: []authorization.ACLEntry{
		{Grantor: proc.Owner, Grantee: authorization.ACLGrantee{Role: proc.Owner}, Privileges: authorization.ExecutePrivilege},
		{Grantor: proc.Owner, Grantee: authorization.ACLGrantee{Role: global.Ref}, Privileges: authorization.ExecutePrivilege, GrantOptions: authorization.ExecutePrivilege},
		{Grantor: proc.Owner, Grantee: authorization.ACLGrantee{Role: scoped.Ref}, Privileges: authorization.ExecutePrivilege},
	}}
	initial := view.State().InitialPrivilege(proc.Address())
	if !reflect.DeepEqual(proc.ACL, want) || initial == nil || initial.PrivType != 'e' || !reflect.DeepEqual(initial.ACL, want) {
		t.Fatal("revoke lost non-PUBLIC creation-default authority", proc.ACL, initial)
	}
	if &initial.ACL.Entries[0] == &proc.ACL.Entries[0] {
		t.Fatal("historical snapshot borrowed current ACL backing")
	}
	enginetest.RoleExecute(t, e, op, "REVOKE EXECUTE ON FUNCTION surgery_acl.heap_force_freeze(regclass,tid[]) FROM surgery_global")
	current := e.CatalogRead().MustProcedure(proc.Address())
	if current == proc || reflect.DeepEqual(current.ACL, proc.ACL) || e.CatalogRead().State().InitialPrivilege(proc.Address()) != initial || !reflect.DeepEqual(proc.ACL, want) || !reflect.DeepEqual(initial.ACL, want) {
		t.Fatal("current ACL change rewrote retained declaration/history backing")
	}
	ref := e.Catalog.Domain.Ref(e.CatalogRead().Epoch(), current.Address())
	if err := extension.ValidatePgsurgeryIdentity(e.CatalogRead(), current, ref); err != nil {
		t.Fatal("current ACL revision lost sealed declaration", err)
	}
}

func TestPgsurgeryFreezeProviderLookalike(t *testing.T) {
	e := engine.New()
	ext := surgeryInstalled(t, e)
	op, done := enginetest.RoleOperation(t, e, "postgres")
	defer done()
	for _, q := range []string{
		"ALTER FUNCTION heap_force_freeze(regclass,tid[]) RENAME TO genuine_freeze",
		"CREATE FUNCTION heap_force_freeze(regclass,tid[]) RETURNS void LANGUAGE SQL AS 'SELECT NULL::void'",
		"ALTER EXTENSION pg_surgery ADD FUNCTION heap_force_freeze(regclass,tid[])",
	} {
		enginetest.RoleExecute(t, e, op, q)
	}
	view := e.CatalogRead()
	ordinary := view.MustProcedure(view.NameAddress(ids.NameKey{ClassID: ids.ProcedureClassID, Namespace: view.MustProcedure(ext.Members[0]).Namespace(), Name: "heap_force_freeze", Signature: "2205,1010"}))
	if ordinary.Extension != ext.Address() || ordinary.Origin != nil || extension.PgsurgeryProviderOf(ordinary) != nil {
		t.Fatal("membership granted installed origin", ordinary)
	}
	if _, err := extension.ProcedureSymbol(view, ordinary); err == nil {
		t.Fatal("ordinary lookalike acquired installed symbol")
	}
	if extension.StockPgsurgery.ValidateProcedure(view, ordinary) == nil {
		t.Fatal("ordinary lookalike acquired surgery authority")
	}
	genuine := view.MustProcedure(ext.Members[0])
	if err := extension.StockPgsurgery.ValidateProcedure(view, genuine); err != nil {
		t.Fatal("mutable membership inventory replaced genuine origin", err)
	}
}
