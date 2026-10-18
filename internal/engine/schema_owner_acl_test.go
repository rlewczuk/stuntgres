package engine

import (
	"context"
	"reflect"
	"strings"
	"stuntgres/internal/engine/authorization"
	"testing"
)

func TestSchemaOwnerTransferExplicitACL(t *testing.T) {
	e := New()
	op, done := roleOperation(t, e, "postgres")
	defer done()
	for _, q := range []string{"CREATE USER former", "CREATE USER successor", "CREATE USER reader", "CREATE USER table_owner"} {
		roleExecute(t, e, op, q)
	}
	former, _ := e.Roles().Lookup("former")
	successor, _ := e.Roles().Lookup("successor")
	reader, _ := e.Roles().Lookup("reader")
	tableOwner, _ := e.Roles().Lookup("table_owner")
	// Restore's private destination authority provisions a differently owned table
	// without granting its owner any schema privileges.
	if err := e.Restore(context.Background(), strings.NewReader("CREATE SCHEMA owned AUTHORIZATION former; CREATE SCHEMA empty_acl AUTHORIZATION former; CREATE SCHEMA absent_acl AUTHORIZATION former; SET SESSION AUTHORIZATION table_owner; CREATE TABLE owned.t(id INT);")); err != nil {
		t.Fatal(err)
	}
	ns := schemaRef(t, e, "owned")
	table := roleTableRef(t, e, "owned.t")
	acl := authorization.ACLValue{Explicit: true, Entries: []authorization.ACLEntry{
		{Grantor: former.Ref, Grantee: authorization.ACLGrantee{Role: former.Ref}, Privileges: authorization.UsagePrivilege | authorization.CreatePrivilege, GrantOptions: authorization.UsagePrivilege},
		{Grantor: successor.Ref, Grantee: authorization.ACLGrantee{Role: successor.Ref}, Privileges: authorization.CreatePrivilege, GrantOptions: authorization.CreatePrivilege},
		{Grantor: former.Ref, Grantee: authorization.ACLGrantee{Role: reader.Ref}, Privileges: authorization.UsagePrivilege},
		{Grantor: reader.Ref, Grantee: authorization.ACLGrantee{Public: true}, Privileges: authorization.CreatePrivilege},
	}}
	if err := e.SetObjectACL(context.Background(), ns, acl); err != nil {
		t.Fatal(err)
	}
	empty := schemaRef(t, e, "empty_acl")
	if err := e.SetObjectACL(context.Background(), empty, authorization.ACLValue{Explicit: true}); err != nil {
		t.Fatal(err)
	}
	retained := e.CatalogRead()
	retainedDescriptor := retained.MustNamespace(ns.Address)
	for _, name := range []string{"owned", "empty_acl", "absent_acl"} {
		roleExecute(t, e, op, "ALTER SCHEMA "+name+" OWNER TO successor")
	}
	want := authorization.ACLValue{Explicit: true, Entries: []authorization.ACLEntry{
		{Grantor: successor.Ref, Grantee: authorization.ACLGrantee{Role: successor.Ref}, Privileges: authorization.UsagePrivilege | authorization.CreatePrivilege, GrantOptions: authorization.UsagePrivilege | authorization.CreatePrivilege},
		{Grantor: successor.Ref, Grantee: authorization.ACLGrantee{Role: reader.Ref}, Privileges: authorization.UsagePrivilege},
		{Grantor: reader.Ref, Grantee: authorization.ACLGrantee{Public: true}, Privileges: authorization.CreatePrivilege},
	}}
	d := e.CatalogRead().MustNamespace(ns.Address)
	if d.owner != successor.Ref || !reflect.DeepEqual(d.acl, want) {
		t.Fatalf("transferred descriptor owner=%v ACL=%+v; want owner=%v ACL=%+v", d.owner, d.acl, successor.Ref, want)
	}
	if retainedDescriptor.owner != former.Ref || !reflect.DeepEqual(retainedDescriptor.acl, acl) {
		t.Fatal("owner transfer mutated retained descriptor")
	}
	if got := e.CatalogRead().MustRelation(table.Address).Owner; got != tableOwner.Ref {
		t.Fatalf("contained table owner=%v want %v", got, tableOwner.Ref)
	}
	for _, tc := range []struct {
		role   string
		rights authorization.Privileges
	}{{"former", authorization.CreatePrivilege}, {"successor", authorization.UsagePrivilege | authorization.CreatePrivilege}, {"reader", authorization.UsagePrivilege | authorization.CreatePrivilege}} {
		roleExecute(t, e, op, "SET SESSION AUTHORIZATION "+tc.role)
		view, err := op.Authorization()
		if err != nil {
			t.Fatal(err)
		}
		rights, err := view.Effective(ns, false)
		if err != nil || rights != tc.rights {
			t.Fatalf("%s rights=%v err=%v want=%v", tc.role, rights, err, tc.rights)
		}
		roleExecute(t, e, op, "RESET SESSION AUTHORIZATION")
	}
	for _, tc := range []struct {
		name     string
		explicit bool
	}{{"empty_acl", true}, {"absent_acl", false}} {
		ref := schemaRef(t, e, tc.name)
		d := e.CatalogRead().MustNamespace(ref.Address)
		if d.acl.Explicit != tc.explicit || len(d.acl.Entries) != 0 {
			t.Fatalf("%s ACL=%+v", tc.name, d.acl)
		}
		roleExecute(t, e, op, "SET SESSION AUTHORIZATION successor")
		view, err := op.Authorization()
		if err != nil {
			t.Fatal(err)
		}
		rights, err := view.Effective(ref, false)
		wantRights := authorization.UsagePrivilege | authorization.CreatePrivilege
		if tc.explicit {
			wantRights = 0
		}
		if err != nil || rights != wantRights {
			t.Fatalf("%s rights=%v err=%v want=%v", tc.name, rights, err, wantRights)
		}
		roleExecute(t, e, op, "RESET SESSION AUTHORIZATION")
	}
	roleExecute(t, e, op, "DROP ROLE former")
	schemaFail(t, e, op, "DROP ROLE successor", "2BP01")
}

func TestSchemaACLOwnerMergeBoundary(t *testing.T) {
	e := New()
	for _, q := range []string{"CREATE USER acl_old", "CREATE USER acl_new", "CREATE USER acl_reader"} {
		run(t, e, q)
	}
	old, _ := e.Roles().Lookup("acl_old")
	next, _ := e.Roles().Lookup("acl_new")
	reader, _ := e.Roles().Lookup("acl_reader")
	zero := authorization.ACLEntry{Grantor: reader.Ref, Grantee: authorization.ACLGrantee{Role: reader.Ref}}
	original := authorization.ACLValue{Explicit: true, Entries: []authorization.ACLEntry{
		{Grantor: reader.Ref, Grantee: authorization.ACLGrantee{Role: old.Ref}, Privileges: authorization.UsagePrivilege},
		zero,
		{Grantor: reader.Ref, Grantee: authorization.ACLGrantee{Role: old.Ref}, Privileges: authorization.CreatePrivilege},
	}}
	want := authorization.ACLValue{Explicit: true, Entries: []authorization.ACLEntry{
		{Grantor: reader.Ref, Grantee: authorization.ACLGrantee{Role: next.Ref}, Privileges: authorization.UsagePrivilege},
		zero,
		{Grantor: reader.Ref, Grantee: authorization.ACLGrantee{Role: next.Ref}, Privileges: authorization.CreatePrivilege},
	}}
	got := authorization.SchemaACLOwner(original, old.Ref, next.Ref)
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("no-new-owner path %+v want %+v", got, want)
	}
	// Only an originally present successor invokes PostgreSQL's duplicate and
	// zero-right cleanup path, even if its own entry has no rights.
	withNext := authorization.ACLValue{Explicit: true, Entries: append(append([]authorization.ACLEntry(nil), original.Entries...), authorization.ACLEntry{Grantor: next.Ref, Grantee: authorization.ACLGrantee{Role: reader.Ref}})}
	got = authorization.SchemaACLOwner(withNext, old.Ref, next.Ref)
	want.Entries = []authorization.ACLEntry{{Grantor: reader.Ref, Grantee: authorization.ACLGrantee{Role: next.Ref}, Privileges: authorization.UsagePrivilege | authorization.CreatePrivilege}}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("merge path %+v want %+v", got, want)
	}
	if original.Entries[0].Grantee.Role != old.Ref || len(withNext.Entries) != 4 || withNext.Entries[2].Privileges != authorization.CreatePrivilege {
		t.Fatal("mutated input ACL")
	}
}
