package engine

import (
	"context"
	"reflect"
	"strings"
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
	acl := ACLValue{Explicit: true, Entries: []ACLEntry{
		{Grantor: former.Ref, Grantee: ACLGrantee{Role: former.Ref}, Privileges: UsagePrivilege | CreatePrivilege, GrantOptions: UsagePrivilege},
		{Grantor: successor.Ref, Grantee: ACLGrantee{Role: successor.Ref}, Privileges: CreatePrivilege, GrantOptions: CreatePrivilege},
		{Grantor: former.Ref, Grantee: ACLGrantee{Role: reader.Ref}, Privileges: UsagePrivilege},
		{Grantor: reader.Ref, Grantee: ACLGrantee{Public: true}, Privileges: CreatePrivilege},
	}}
	if err := e.SetObjectACL(context.Background(), ns, acl); err != nil {
		t.Fatal(err)
	}
	empty := schemaRef(t, e, "empty_acl")
	if err := e.SetObjectACL(context.Background(), empty, ACLValue{Explicit: true}); err != nil {
		t.Fatal(err)
	}
	retained := e.CatalogRead()
	retainedDescriptor := retained.state.descriptors[ns.Address].(*namespaceDescriptor)
	for _, name := range []string{"owned", "empty_acl", "absent_acl"} {
		roleExecute(t, e, op, "ALTER SCHEMA "+name+" OWNER TO successor")
	}
	want := ACLValue{Explicit: true, Entries: []ACLEntry{
		{Grantor: successor.Ref, Grantee: ACLGrantee{Role: successor.Ref}, Privileges: UsagePrivilege | CreatePrivilege, GrantOptions: UsagePrivilege | CreatePrivilege},
		{Grantor: successor.Ref, Grantee: ACLGrantee{Role: reader.Ref}, Privileges: UsagePrivilege},
		{Grantor: reader.Ref, Grantee: ACLGrantee{Public: true}, Privileges: CreatePrivilege},
	}}
	d := e.CatalogRead().state.descriptors[ns.Address].(*namespaceDescriptor)
	if d.owner != successor.Ref || !reflect.DeepEqual(d.acl, want) {
		t.Fatalf("transferred descriptor owner=%v ACL=%+v; want owner=%v ACL=%+v", d.owner, d.acl, successor.Ref, want)
	}
	if retainedDescriptor.owner != former.Ref || !reflect.DeepEqual(retainedDescriptor.acl, acl) {
		t.Fatal("owner transfer mutated retained descriptor")
	}
	if got := e.CatalogRead().state.descriptors[table.Address].(*relationDescriptor).owner; got != tableOwner.Ref {
		t.Fatalf("contained table owner=%v want %v", got, tableOwner.Ref)
	}
	for _, tc := range []struct {
		role   string
		rights Privileges
	}{{"former", CreatePrivilege}, {"successor", UsagePrivilege | CreatePrivilege}, {"reader", UsagePrivilege | CreatePrivilege}} {
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
		d := e.CatalogRead().state.descriptors[ref.Address].(*namespaceDescriptor)
		if d.acl.Explicit != tc.explicit || len(d.acl.Entries) != 0 {
			t.Fatalf("%s ACL=%+v", tc.name, d.acl)
		}
		roleExecute(t, e, op, "SET SESSION AUTHORIZATION successor")
		view, err := op.Authorization()
		if err != nil {
			t.Fatal(err)
		}
		rights, err := view.Effective(ref, false)
		wantRights := UsagePrivilege | CreatePrivilege
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
	zero := ACLEntry{Grantor: reader.Ref, Grantee: ACLGrantee{Role: reader.Ref}}
	original := ACLValue{Explicit: true, Entries: []ACLEntry{
		{Grantor: reader.Ref, Grantee: ACLGrantee{Role: old.Ref}, Privileges: UsagePrivilege},
		zero,
		{Grantor: reader.Ref, Grantee: ACLGrantee{Role: old.Ref}, Privileges: CreatePrivilege},
	}}
	want := ACLValue{Explicit: true, Entries: []ACLEntry{
		{Grantor: reader.Ref, Grantee: ACLGrantee{Role: next.Ref}, Privileges: UsagePrivilege},
		zero,
		{Grantor: reader.Ref, Grantee: ACLGrantee{Role: next.Ref}, Privileges: CreatePrivilege},
	}}
	got := schemaACLOwner(original, old.Ref, next.Ref)
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("no-new-owner path %+v want %+v", got, want)
	}
	// Only an originally present successor invokes PostgreSQL's duplicate and
	// zero-right cleanup path, even if its own entry has no rights.
	withNext := ACLValue{Explicit: true, Entries: append(append([]ACLEntry(nil), original.Entries...), ACLEntry{Grantor: next.Ref, Grantee: ACLGrantee{Role: reader.Ref}})}
	got = schemaACLOwner(withNext, old.Ref, next.Ref)
	want.Entries = []ACLEntry{{Grantor: reader.Ref, Grantee: ACLGrantee{Role: next.Ref}, Privileges: UsagePrivilege | CreatePrivilege}}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("merge path %+v want %+v", got, want)
	}
	if original.Entries[0].Grantee.Role != old.Ref || len(withNext.Entries) != 4 || withNext.Entries[2].Privileges != CreatePrivilege {
		t.Fatal("mutated input ACL")
	}
}
