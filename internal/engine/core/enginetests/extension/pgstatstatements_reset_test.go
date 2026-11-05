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

func TestPgStatStatementsInstallationResetACL(t *testing.T) {
	e := engine.New()
	for _, q := range []string{
		"CREATE ROLE reset_global", "CREATE ROLE reset_scoped", "CREATE SCHEMA reset_acl",
		"ALTER DEFAULT PRIVILEGES GRANT EXECUTE ON FUNCTIONS TO reset_global WITH GRANT OPTION",
		"ALTER DEFAULT PRIVILEGES IN SCHEMA reset_acl GRANT EXECUTE ON FUNCTIONS TO reset_scoped",
	} {
		enginetest.Run(t, e, q)
	}
	op, done := enginetest.RoleOperation(t, e, "postgres")
	defer done()
	enginetest.RoleExecute(t, e, op, "CREATE EXTENSION pg_stat_statements SCHEMA reset_acl")
	view := e.CatalogRead()
	ext := view.MustExtension(view.NameAddress(ids.NameKey{ClassID: ids.ExtensionClassID, Name: "pg_stat_statements"}))
	if len(ext.Installation.Members) != 3 {
		t.Fatal("sealed declaration count", ext.Installation)
	}
	proc := view.MustProcedure(ext.Installation.Members[2])
	if proc.Name() != "pg_stat_statements_reset" || proc.Signature != "26,26,20,16" || proc.Result != 1184 || !proc.Strict || proc.Installed.Defaults != 4 || proc.LiteralDefaults.Len() != 4 {
		t.Fatal("missing real reset declaration", proc)
	}
	global, _ := e.Roles().Lookup("reset_global")
	scoped, _ := e.Roles().Lookup("reset_scoped")
	want := authorization.ACLValue{Explicit: true, Entries: []authorization.ACLEntry{
		{Grantor: proc.Owner, Grantee: authorization.ACLGrantee{Role: proc.Owner}, Privileges: authorization.ExecutePrivilege},
		{Grantor: proc.Owner, Grantee: authorization.ACLGrantee{Role: global.Ref}, Privileges: authorization.ExecutePrivilege, GrantOptions: authorization.ExecutePrivilege},
		{Grantor: proc.Owner, Grantee: authorization.ACLGrantee{Role: scoped.Ref}, Privileges: authorization.ExecutePrivilege},
	}}
	initial := view.State().InitialPrivilege(proc.Address())
	if !reflect.DeepEqual(proc.ACL, want) || initial == nil || initial.PrivType != 'e' || !reflect.DeepEqual(initial.ACL, want) || &initial.ACL.Entries[0] == &proc.ACL.Entries[0] {
		t.Fatal("script revoke lost named creation grants or independent initial ACL", proc.ACL, initial)
	}
	for slot, name := range []string{"pg_stat_statements", "pg_stat_statements_info"} {
		reader := view.MustProcedure(ext.Installation.Members[slot])
		if reader.Name() != name || reader.Origin.Declaration != slot || reader.LiteralDefaults != nil {
			t.Fatal("reset changed incumbent declaration identity", reader)
		}
		public := false
		for _, entry := range reader.ACL.Entries {
			public = public || entry.Grantee.Public && entry.Privileges&authorization.ExecutePrivilege != 0
		}
		// A non-explicit reader ACL retains ordinary PUBLIC function defaults.
		if reader.ACL.Explicit && !public {
			t.Fatal("reset revoke leaked into reader", reader.ACL)
		}
	}
	enginetest.RoleExecute(t, e, op, "REVOKE EXECUTE ON FUNCTION reset_acl.pg_stat_statements_reset(oid,oid,bigint,boolean) FROM reset_global")
	current := e.CatalogRead().MustProcedure(proc.Address())
	if current == proc || reflect.DeepEqual(current.ACL, proc.ACL) || e.CatalogRead().State().InitialPrivilege(proc.Address()) != initial || !reflect.DeepEqual(initial.ACL, want) || !reflect.DeepEqual(proc.ACL, want) || current.LiteralDefaults != proc.LiteralDefaults {
		t.Fatal("current ACL revision mutated immutable defaults/history backing")
	}
	if err := extension.StockPgStatStatements.ValidateProcedure(e.CatalogRead(), current); err != nil {
		t.Fatal("ACL revision lost genuine declaration", err)
	}
}
