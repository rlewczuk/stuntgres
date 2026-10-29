package schemacmds_test

import (
	"reflect"
	"testing"

	"stuntgres/internal/engine"
	"stuntgres/internal/engine/access"
	"stuntgres/internal/engine/authorization"
	"stuntgres/internal/engine/enginetest"
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/engine/session"
)

func TestPGInitPrivsLifecycle(t *testing.T) {
	e := engine.New()
	for _, sql := range []string{
		"CREATE ROLE ip_reader", "CREATE EXTENSION pageinspect",
		"CREATE TABLE ip_member (a integer, b integer, c integer)",
		"GRANT SELECT ON ip_member TO ip_reader",
		"GRANT UPDATE (b), SELECT (c) ON ip_member TO ip_reader",
	} {
		enginetest.Run(t, e, sql)
	}
	address := e.CatalogRead().NameAddress(ids.NameKey{ClassID: ids.RelationClassID, Namespace: ids.ObjectAddress{ClassID: ids.NamespaceClassID, ObjectID: ids.PublicNamespaceOID}, Name: "ip_member"})
	before := e.CatalogRead().MustRelation(address)
	if e.CatalogRead().State().InitialPrivilege(address) != nil {
		t.Fatal("ordinary creation/grants invented initial history")
	}
	enginetest.Run(t, e, "ALTER EXTENSION pageinspect ADD TABLE ip_member")
	retained := e.CatalogRead()
	record := retained.State().InitialPrivilege(address)
	if record == nil || record.PrivType != 'e' || !reflect.DeepEqual(record.ACL, before.Security.ACL) {
		t.Fatal("ADD did not capture full raw table ACL", record)
	}
	column := address
	column.SubID = 2
	oldColumn := retained.State().InitialPrivilege(column)
	if oldColumn == nil || !reflect.DeepEqual(oldColumn.ACL, before.Security.ColumnACL[2]) {
		t.Fatal("ADD lost stable column ACL identity", oldColumn)
	}
	enginetest.Run(t, e, "GRANT INSERT ON ip_member TO ip_reader")
	if e.CatalogRead().State().InitialPrivilege(address) != record {
		t.Fatal("ordinary grant refreshed initial snapshot")
	}
	enginetest.Run(t, e, "ALTER TABLE ip_member DROP COLUMN a")
	if e.CatalogRead().State().InitialPrivilege(column) != oldColumn {
		t.Fatal("dropping an earlier slot renumbered initial column history")
	}
	enginetest.Run(t, e, "ALTER TABLE ip_member DROP COLUMN b")
	if e.CatalogRead().State().InitialPrivilege(column) != nil {
		t.Fatal("dropped-column snapshot survived")
	}
	enginetest.Run(t, e, "ALTER EXTENSION pageinspect DROP TABLE ip_member")
	for a := range e.CatalogRead().State().InitialPrivileges() {
		if a.ClassID == address.ClassID && a.ObjectID == address.ObjectID {
			t.Fatal("detach retained table/column history", a)
		}
	}
	enginetest.Run(t, e, "ALTER EXTENSION pageinspect ADD TABLE ip_member")
	fresh := e.CatalogRead().State().InitialPrivilege(address)
	if fresh == nil || reflect.DeepEqual(fresh.ACL, record.ACL) {
		t.Fatal("re-ADD reused old history instead of new raw ACL", fresh)
	}
	enginetest.Run(t, e, "DROP EXTENSION pageinspect CASCADE")
	for a := range e.CatalogRead().State().InitialPrivileges() {
		if a.ClassID == address.ClassID && a.ObjectID == address.ObjectID {
			t.Fatal("extension cascade retained history", a)
		}
	}
	if retained.State().InitialPrivilege(address) != record || retained.State().InitialPrivilege(column) != oldColumn || !reflect.DeepEqual(oldColumn.ACL, before.Security.ColumnACL[2]) {
		t.Fatal("later lifecycle mutated retained history")
	}
}

func TestPGInitPrivsRoleLifecycle(t *testing.T) {
	e := engine.New()
	op, done := enginetest.RoleOperation(t, e, "postgres")
	defer done()
	run := func(sql string) { enginetest.RoleExecute(t, e, op, sql) }
	for _, sql := range []string{
		"CREATE ROLE ip_old", "CREATE ROLE ip_next", "CREATE ROLE ip_reader",
		"GRANT CREATE ON SCHEMA public TO ip_old, ip_next",
		"CREATE EXTENSION pageinspect", "SET ROLE ip_old",
		"CREATE TABLE ip_history (a integer)", "GRANT SELECT ON ip_history TO ip_reader",
		"GRANT SELECT ON ip_history TO ip_next WITH GRANT OPTION",
		"RESET ROLE", "ALTER EXTENSION pageinspect ADD TABLE ip_history",
	} {
		run(sql)
	}
	view := e.CatalogRead()
	address := view.NameAddress(ids.NameKey{ClassID: ids.RelationClassID, Namespace: ids.ObjectAddress{ClassID: ids.NamespaceClassID, ObjectID: ids.PublicNamespaceOID}, Name: "ip_history"})
	old, _ := e.Roles().Lookup("ip_old")
	next, _ := e.Roles().Lookup("ip_next")
	reader, _ := e.Roles().Lookup("ip_reader")
	record := view.State().InitialPrivilege(address)
	if record == nil {
		t.Fatal("missing historical role fixture")
	}
	run("ALTER TABLE ip_history OWNER TO ip_next")
	run("REVOKE SELECT ON ip_history FROM ip_reader")
	if e.CatalogRead().State().InitialPrivilege(address) != record {
		t.Fatal("bare ALTER OWNER or REVOKE rewrote history")
	}
	var dependencies []access.RoleDependency
	access.InitialRoleDependencies(e.CatalogRead(), old.Ref, &dependencies)
	if len(dependencies) != 1 || dependencies[0].Address != address || dependencies[0].Kind != 'i' {
		t.Fatal("historical owner missing from dependency inventory", dependencies)
	}
	tx, err := e.Begin(op, session.TxOptions{})
	if err != nil {
		t.Fatal(err)
	}
	run("REASSIGN OWNED BY ip_old TO ip_next")
	e.Mu.Lock()
	private := e.PrivateCatalogLocked(session.TxOwnerOf(tx).Catalog)
	e.Mu.Unlock()
	if private.State().InitialPrivilege(address) == record || e.CatalogRead().State().InitialPrivilege(address) != record {
		t.Fatal("history-only rewrite did not remain private")
	}
	if _, err := e.Rollback(op, tx); err != nil {
		t.Fatal(err)
	}
	if e.CatalogRead().State().InitialPrivilege(address) != record {
		t.Fatal("rollback published historical rewrite")
	}
	run("REASSIGN OWNED BY ip_old TO ip_next")
	rewritten := e.CatalogRead().State().InitialPrivilege(address)
	want := authorization.ACLValue{Explicit: true, Entries: []authorization.ACLEntry{
		{Grantor: next.Ref, Grantee: authorization.ACLGrantee{Role: next.Ref}, Privileges: authorization.TableGrantPrivileges, GrantOptions: authorization.SelectPrivilege},
		{Grantor: next.Ref, Grantee: authorization.ACLGrantee{Role: reader.Ref}, Privileges: authorization.SelectPrivilege},
	}}
	if rewritten == nil || rewritten.PrivType != record.PrivType || !reflect.DeepEqual(rewritten.ACL, want) {
		t.Fatal("snapshot-only REASSIGN lost historical ACL", rewritten)
	}
	if access.InitialRoleDependencies(e.CatalogRead(), old.Ref, nil) {
		t.Fatal("REASSIGN retained historical old principal")
	}
	run("DROP OWNED BY ip_reader")
	if access.InitialRoleDependencies(e.CatalogRead(), reader.Ref, nil) {
		t.Fatal("DROP OWNED retained snapshot-only grantee")
	}
	run("DROP ROLE ip_reader")
	run("DROP OWNED BY ip_old")
	run("DROP ROLE ip_old")
	if view.State().InitialPrivilege(address) != record || record.ACL.Entries[0].Grantor != old.Ref {
		t.Fatal("role lifecycle mutated retained snapshot authority")
	}
}

func TestPGInitPrivsProviderEvents(t *testing.T) {
	e := engine.New()
	enginetest.Run(t, e, "CREATE EXTENSION amcheck")
	view := e.CatalogRead()
	extension := view.MustExtension(view.NameAddress(ids.NameKey{ClassID: ids.ExtensionClassID, Name: "amcheck"}))
	count := 0
	for _, address := range extension.Members {
		if address.ClassID != ids.ProcedureClassID {
			continue
		}
		proc := view.MustProcedure(address)
		record := view.State().InitialPrivilege(address)
		want := authorization.ACLValue{Explicit: true, Entries: []authorization.ACLEntry{{Grantor: proc.Owner, Grantee: authorization.ACLGrantee{Role: proc.Owner}, Privileges: authorization.ExecutePrivilege}}}
		if record == nil || record.PrivType != 'e' || !reflect.DeepEqual(record.ACL, want) {
			t.Fatal("script REVOKE did not record exact owner-only ACL", proc.Name(), record)
		}
		count++
	}
	if count != 8 {
		t.Fatal("wrong implemented amcheck inventory", count)
	}
	enginetest.Run(t, e, "ALTER EXTENSION amcheck UPDATE TO '1.5'")
	for address, record := range view.State().InitialPrivileges() {
		if e.CatalogRead().State().InitialPrivilege(address) != record {
			t.Fatal("same-version UPDATE refreshed historical record", address)
		}
	}
}
