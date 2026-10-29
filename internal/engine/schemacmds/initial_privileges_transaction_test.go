package schemacmds_test

import (
	"reflect"
	"testing"

	"stuntgres/internal/engine"
	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/enginetest"
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/engine/session"
)

// An own GRANT advances private role authority, not its committed predecessor.
// Extension journal fences must retain the latter across detach/re-ADD, while
// CatalogAuthority continues checking the statement's actual private authority.
func TestPGInitPrivsExtensionPrivateAuthority(t *testing.T) {
	e := engine.New()
	op, done := enginetest.RoleOperation(t, e, "postgres")
	defer done()
	run := func(sql string) { enginetest.RoleExecute(t, e, op, sql) }
	for _, sql := range []string{
		"CREATE ROLE ip_private_reader", "CREATE EXTENSION pageinspect",
		"CREATE TABLE ip_private_member (a integer)",
		"GRANT SELECT ON ip_private_member TO ip_private_reader",
	} {
		run(sql)
	}
	base := e.CatalogRead()
	address := base.NameAddress(ids.NameKey{ClassID: ids.RelationClassID, Namespace: ids.ObjectAddress{ClassID: ids.NamespaceClassID, ObjectID: ids.PublicNamespaceOID}, Name: "ip_private_member"})
	publishedRoles := e.RoleStore.Published
	tx, err := e.Begin(op, session.TxOptions{})
	if err != nil {
		t.Fatal(err)
	}
	defer e.Rollback(op, tx)
	private := func() catalog.Read {
		e.Mu.Lock()
		defer e.Mu.Unlock()
		return e.PrivateCatalogLocked(session.TxOwnerOf(tx).Catalog)
	}
	run("ALTER EXTENSION pageinspect ADD TABLE ip_private_member")
	initial := private().State().InitialPrivilege(address)
	if initial == nil {
		t.Fatal("private ADD did not transfer initial delta")
	}
	run("GRANT INSERT ON ip_private_member TO ip_private_reader")
	changes := session.TxOwnerOf(tx).Catalog
	if changes.RolesBase != publishedRoles || changes.Roles == nil || changes.Roles.Revision == publishedRoles.Revision {
		t.Fatal("GRANT fixture did not distinguish committed/private role authority")
	}
	run("ALTER EXTENSION pageinspect DROP TABLE ip_private_member")
	run("ALTER EXTENSION pageinspect ADD TABLE ip_private_member")
	readded := private().State().InitialPrivilege(address)
	if readded == nil || readded == initial || reflect.DeepEqual(readded.ACL, initial.ACL) {
		t.Fatal("private re-ADD lost fresh ACL history", readded)
	}
	if e.CatalogRead().State() != base.State() || e.CatalogRead().State().InitialPrivilege(address) != nil || e.RoleStore.Published != publishedRoles {
		t.Fatal("private membership/ACL changes escaped transaction journal")
	}
	run("SAVEPOINT ip_private_point")
	run("GRANT UPDATE ON ip_private_member TO ip_private_reader")
	run("ALTER EXTENSION pageinspect DROP TABLE ip_private_member")
	run("ALTER EXTENSION pageinspect ADD TABLE ip_private_member")
	if current := private().State().InitialPrivilege(address); current == nil || current == readded || reflect.DeepEqual(current.ACL, readded.ACL) {
		t.Fatal("second private role revision did not capture new ACL history")
	}
	run("ROLLBACK TO SAVEPOINT ip_private_point")
	if private().State().InitialPrivilege(address) != readded {
		t.Fatal("savepoint rollback failed to restore immutable snapshot backing")
	}
	if _, err := e.Commit(op, tx); err != nil {
		t.Fatal(err)
	}
	if e.CatalogRead().State().InitialPrivilege(address) != readded || base.State().InitialPrivilege(address) != nil {
		t.Fatal("commit lost private history or mutated retained base")
	}
}
