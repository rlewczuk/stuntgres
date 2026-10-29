package rolecmds_test

import (
	"context"
	"strings"
	"testing"

	"stuntgres/internal/engine"
	"stuntgres/internal/engine/access"
	"stuntgres/internal/engine/authorization"
	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/core"
	"stuntgres/internal/engine/enginetest"
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/engine/session"
	"stuntgres/internal/parser"
)

func TestMembershipInheritedOwnerAndColumnACL(t *testing.T) {
	e := engine.New()
	op, done := roleOperation(t, e, "postgres")
	defer done()
	for _, q := range []string{"CREATE USER owner", "CREATE USER reader", "CREATE ROLE middle", "CREATE ROLE rights", "SET SESSION AUTHORIZATION owner"} {
		roleExecute(t, e, op, q)
	}
	withPublicSchemaUnitOwner(t, e, "owner", func() {
		roleExecute(t, e, op, "CREATE TABLE secret(a INT,b INT)")
	})
	for _, q := range []string{"RESET SESSION AUTHORIZATION", "GRANT owner TO middle", "GRANT middle TO reader"} {
		roleExecute(t, e, op, q)
	}
	ref := enginetest.RoleTableRef(t, e, "secret")
	roleExecute(t, e, op, "SET SESSION AUTHORIZATION reader")
	retained, err := core.OperationAuthorization(op)
	if err != nil {
		t.Fatal(err)
	}
	if err := retained.Check(access.Requirement{Object: ref, OwnerOnly: true}); err != nil {
		t.Fatal("inherited owner", err)
	}
	if err := enginetest.SetObjectACL(e, context.Background(), ref, authorization.ACLValue{Explicit: true}); err != nil {
		t.Fatal(err)
	}
	view, err := core.OperationAuthorization(op)
	if err != nil {
		t.Fatal(err)
	}
	if err := view.Check(access.Requirement{Object: ref, Privileges: authorization.SelectPrivilege}); err == nil {
		t.Fatal("explicit empty inherited owner use")
	}
	if err := view.Check(access.Requirement{Object: ref, OwnerOnly: true}); err != nil {
		t.Fatal(err)
	}
	mask := authorization.TableGrantPrivileges
	options, err := view.Effective(ref, true)
	if err != nil || options != mask {
		t.Fatal("inherited implicit options", options, err)
	}
	owner, _ := e.Roles().Lookup("owner")
	rights, _ := e.Roles().Lookup("rights")
	acl := authorization.ACLValue{Explicit: true, Entries: []authorization.ACLEntry{{Grantor: owner.Ref, Grantee: authorization.ACLGrantee{Public: true}, Privileges: authorization.SelectPrivilege}, {Grantor: owner.Ref, Grantee: authorization.ACLGrantee{Role: rights.Ref}, Privileges: authorization.InsertPrivilege, GrantOptions: authorization.InsertPrivilege}}}
	if err := enginetest.SetObjectACL(e, context.Background(), ref, acl); err != nil {
		t.Fatal(err)
	}
	roleExecute(t, e, op, "RESET SESSION AUTHORIZATION")
	roleExecute(t, e, op, "GRANT rights TO reader")
	roleExecute(t, e, op, "REVOKE INHERIT OPTION FOR middle FROM reader")
	roleExecute(t, e, op, "SET SESSION AUTHORIZATION reader")
	view, err = core.OperationAuthorization(op)
	if err != nil {
		t.Fatal(err)
	}
	got, err := view.Effective(ref, false)
	if err != nil || got != authorization.SelectPrivilege|authorization.InsertPrivilege {
		t.Fatal("PUBLIC + inherited ACL", got, err)
	}
	got, err = view.Effective(ref, true)
	if err != nil || got != authorization.InsertPrivilege {
		t.Fatal("inherited grant options", got, err)
	}
	column := ref
	column.Address.SubID = 1
	if err := enginetest.SetObjectACL(e, context.Background(), column, authorization.ACLValue{Explicit: true, Entries: []authorization.ACLEntry{{Grantor: owner.Ref, Grantee: authorization.ACLGrantee{Role: rights.Ref}, Privileges: authorization.UpdatePrivilege}}}); err != nil {
		t.Fatal(err)
	}
	view, err = core.OperationAuthorization(op)
	if err != nil {
		t.Fatal(err)
	}
	if err := view.Check(access.Requirement{Object: column, Privileges: authorization.SelectPrivilege | authorization.InsertPrivilege | authorization.UpdatePrivilege}); err != nil {
		t.Fatal("exact column aggregation", err)
	}
	if err := view.Check(access.Requirement{Object: ref, Privileges: authorization.UpdatePrivilege}); err == nil {
		t.Fatal("column grants leaked into table mask")
	}
	other := column
	other.Address.SubID = 2
	if err := view.Check(access.Requirement{Object: other, Privileges: authorization.UpdatePrivilege}); err == nil {
		t.Fatal("other column leaked")
	}
	if err := retained.Check(access.Requirement{Object: ref, OwnerOnly: true}); err != nil {
		t.Fatal("retained authority changed", err)
	}
}

// withPublicSchemaUnitOwner provisions only a positive CREATE or owner-successor
// precondition. It leaves the tested operation's identity and object ACLs alone.
func withPublicSchemaUnitOwner(t *testing.T, e *core.Engine, owner string, body func()) {
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
	sess, err := enginetest.OpenSession(context.Background(), e, session.SessionStart{ID: ids.SessionID(testSessionID.Add(1))}, login)
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
	roleExecute(t, e, admin, "ALTER SCHEMA public OWNER TO "+quote(owner))
	body()
}
