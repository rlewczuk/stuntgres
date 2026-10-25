package indexcmds_test

import (
	"context"
	"strings"
	"sync/atomic"
	"testing"

	"stuntgres/internal/engine"
	"stuntgres/internal/engine/authorization"
	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/enginetest"
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/engine/session"
	"stuntgres/internal/parser"
)

func TestBtreeIndexOwnerAndNamespaceAuthority(t *testing.T) {
	e := engine.New()
	op, done := enginetest.RoleOperation(t, e, "postgres")
	defer done()
	enginetest.RoleExecute(t, e, op, "CREATE USER owner")
	enginetest.RoleExecute(t, e, op, "CREATE USER reader")
	enginetest.RoleExecute(t, e, op, "SET SESSION AUTHORIZATION owner")
	withPublicSchemaUnitOwner(t, e, "owner", func() {
		enginetest.RoleExecute(t, e, op, "CREATE TABLE t(n INT)")
		enginetest.RoleExecute(t, e, op, "CREATE INDEX idx ON t(n)")
	})
	ref := enginetest.RoleTableRef(t, e, "t")
	owner := e.RoleStore.Published.Root.Read(authorization.PrincipalRef{}).LookupRef("owner")
	reader := e.RoleStore.Published.Root.Read(authorization.PrincipalRef{}).LookupRef("reader")
	if err := enginetest.SetObjectACL(e, context.Background(), ref, authorization.ACLValue{Explicit: true, Entries: []authorization.ACLEntry{{Grantor: owner, Grantee: authorization.ACLGrantee{Role: reader}, Privileges: authorization.SelectPrivilege | authorization.InsertPrivilege}}}); err != nil {
		t.Fatal(err)
	}
	enginetest.RoleExecute(t, e, op, "RESET SESSION AUTHORIZATION")
	enginetest.RoleExecute(t, e, op, "SET SESSION AUTHORIZATION reader")
	withPublicSchemaUnitOwner(t, e, "reader", func() {
		enginetest.RoleError(t, e, op, "CREATE INDEX forbidden ON t(n)", "42501", "must be owner of table t", "")
	})
	enginetest.RoleExecute(t, e, op, "RESET SESSION AUTHORIZATION")
	ns := e.Catalog.Domain.Ref(e.Catalog.State.Epoch(), ids.ObjectAddress{ClassID: ids.NamespaceClassID, ObjectID: ids.PublicNamespaceOID})
	if err := enginetest.SetObjectACL(e, context.Background(), ns, authorization.ACLValue{Explicit: true, Entries: []authorization.ACLEntry{{Grantor: e.RoleStore.Published.Root.Read(authorization.PrincipalRef{}).LookupRef("postgres"), Grantee: authorization.ACLGrantee{Public: true}, Privileges: authorization.UsagePrivilege}}}); err != nil {
		t.Fatal(err)
	}
	enginetest.RoleExecute(t, e, op, "SET SESSION AUTHORIZATION owner")
	enginetest.RoleError(t, e, op, "CREATE INDEX forbidden ON t(n)", "42501", "permission denied for schema public", "")
	if len(e.Storage.IndexRoots) != 1 {
		t.Fatal("denied index leaked roots")
	}
}
func TestBtreeIndexPrivateTypeSavepointAndDrop(t *testing.T) {
	e := engine.New()
	s := enginetest.StartSession(t, e, nil)
	enginetest.SessionSQL(t, s, "CREATE TABLE t(n INT,s TEXT)")
	enginetest.SessionSQL(t, s, "INSERT INTO t VALUES(1,'a'),(2,'b'),(10,'c')")
	enginetest.SessionSQL(t, s, "CREATE INDEX ni ON t(n)")
	enginetest.SessionSQL(t, s, "CREATE INDEX si ON t(s)")
	old := btreeWantKeys(t, e, "ni", "1", "2", "10")
	tx := enginetest.TransactionBegin(t, s)
	enginetest.SessionSQL(t, s, "SAVEPOINT before_type")
	enginetest.SessionSQL(t, s, "ALTER TABLE t ALTER n TYPE TEXT USING n::text")
	if _, root := btreeRoot(t, e, "ni"); root != old {
		t.Fatal("private type published root")
	}
	enginetest.SessionSQL(t, s, "ROLLBACK TO before_type")
	enginetest.TransactionEnd(t, s, tx, true)
	btreeWantKeys(t, e, "ni", "1", "2", "10")
	tx = enginetest.TransactionBegin(t, s)
	enginetest.SessionSQL(t, s, "ALTER TABLE t ALTER n TYPE TEXT USING n::text")
	enginetest.TransactionEnd(t, s, tx, true)
	btreeWantKeys(t, e, "ni", "1", "10", "2")
	tx = enginetest.TransactionBegin(t, s)
	enginetest.SessionSQL(t, s, "ALTER TABLE t DROP s")
	enginetest.TransactionEnd(t, s, tx, false)
	btreeWantKeys(t, e, "si", "a", "b", "c")
	tx = enginetest.TransactionBegin(t, s)
	enginetest.SessionSQL(t, s, "ALTER TABLE t DROP s")
	enginetest.TransactionEnd(t, s, tx, true)
	if len(e.Storage.IndexRoots) != 1 {
		t.Fatal("private drop left orphan root")
	}
}

// withPublicSchemaUnitOwner provisions only a positive CREATE or owner-successor
// precondition. It leaves the tested operation's identity and object ACLs alone.
func withPublicSchemaUnitOwner(t *testing.T, e *engine.Engine, owner string, body func()) {
	t.Helper()
	view := e.CatalogRead()
	addresses := view.Lookup(ids.NameKey{ClassID: ids.NamespaceClassID, Name: "public"})
	if len(addresses) != 1 {
		t.Fatal("public schema fixture requires one public namespace")
	}
	descriptor, ok := view.Descriptor(addresses[0])
	if !ok {
		t.Fatal("public schema fixture requires its namespace descriptor")
	}
	namespace, ok := descriptor.(*catalog.Namespace)
	if !ok {
		t.Fatal("public schema fixture has a non-namespace descriptor")
	}
	roles := e.Roles()
	prior, err := roles.Descriptor(namespace.Owner)
	if err != nil {
		t.Fatal(err)
	}
	bootstrap, err := roles.Descriptor(e.RoleStore.Domain.Principal(10))
	if err != nil {
		t.Fatal(err)
	}
	login, err := e.ResolveLogin(bootstrap.Name)
	if err != nil {
		t.Fatal(err)
	}
	sess, err := e.StartSession(context.Background(), session.SessionStart{ID: ids.SessionID(testSessionID.Add(1)), Authenticated: login})
	if err != nil {
		t.Fatal(err)
	}
	defer func() {
		sess.RequestEnd(session.CleanClose)
		if err := sess.WaitEnd(context.Background()); err != nil {
			t.Error("closing public schema fixture session:", err)
		}
	}()
	admin, err := sess.Acquire(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	defer admin.Done()
	quote := func(name string) string { return `"` + strings.ReplaceAll(name, `"`, `""`) + `"` }
	// Restore while the borrowed operation and the prior owner are still live,
	// including when the body terminates through testing.T.Fatal.
	defer func() {
		restore, err := parser.Parse("ALTER SCHEMA public OWNER TO " + quote(prior.Name))
		if err != nil {
			t.Error("parsing public schema owner restoration:", err)
			return
		}
		if len(restore) != 1 {
			t.Errorf("expected single public schema owner restoration statement, got %d", len(restore))
			return
		}
		if _, err := e.Execute(admin, restore[0], 0, nil); err != nil {
			t.Error("restoring public schema owner:", err)
		}
	}()
	enginetest.RoleExecute(t, e, admin, "ALTER SCHEMA public OWNER TO "+quote(owner))
	body()
}

var testSessionID atomic.Uint64
