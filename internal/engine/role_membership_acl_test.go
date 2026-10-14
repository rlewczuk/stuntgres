package engine

import (
	"context"
	"stuntgres/internal/engine/authorization"
	"testing"
)

func TestMembershipInheritedOwnerAndColumnACL(t *testing.T) {
	e := New()
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
	ref := roleTableRef(t, e, "secret")
	roleExecute(t, e, op, "SET SESSION AUTHORIZATION reader")
	retained, err := op.Authorization()
	if err != nil {
		t.Fatal(err)
	}
	if err := retained.Check(AccessRequirement{Object: ref, OwnerOnly: true}); err != nil {
		t.Fatal("inherited owner", err)
	}
	if err := e.SetObjectACL(context.Background(), ref, authorization.ACLValue{Explicit: true}); err != nil {
		t.Fatal(err)
	}
	view, err := op.Authorization()
	if err != nil {
		t.Fatal(err)
	}
	if err := view.Check(AccessRequirement{Object: ref, Privileges: authorization.SelectPrivilege}); err == nil {
		t.Fatal("explicit empty inherited owner use")
	}
	if err := view.Check(AccessRequirement{Object: ref, OwnerOnly: true}); err != nil {
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
	if err := e.SetObjectACL(context.Background(), ref, acl); err != nil {
		t.Fatal(err)
	}
	roleExecute(t, e, op, "RESET SESSION AUTHORIZATION")
	roleExecute(t, e, op, "GRANT rights TO reader")
	roleExecute(t, e, op, "REVOKE INHERIT OPTION FOR middle FROM reader")
	roleExecute(t, e, op, "SET SESSION AUTHORIZATION reader")
	view, err = op.Authorization()
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
	if err := e.SetObjectACL(context.Background(), column, authorization.ACLValue{Explicit: true, Entries: []authorization.ACLEntry{{Grantor: owner.Ref, Grantee: authorization.ACLGrantee{Role: rights.Ref}, Privileges: authorization.UpdatePrivilege}}}); err != nil {
		t.Fatal(err)
	}
	view, err = op.Authorization()
	if err != nil {
		t.Fatal(err)
	}
	if err := view.Check(AccessRequirement{Object: column, Privileges: authorization.SelectPrivilege | authorization.InsertPrivilege | authorization.UpdatePrivilege}); err != nil {
		t.Fatal("exact column aggregation", err)
	}
	if err := view.Check(AccessRequirement{Object: ref, Privileges: authorization.UpdatePrivilege}); err == nil {
		t.Fatal("column grants leaked into table mask")
	}
	other := column
	other.Address.SubID = 2
	if err := view.Check(AccessRequirement{Object: other, Privileges: authorization.UpdatePrivilege}); err == nil {
		t.Fatal("other column leaked")
	}
	if err := retained.Check(AccessRequirement{Object: ref, OwnerOnly: true}); err != nil {
		t.Fatal("retained authority changed", err)
	}
}
