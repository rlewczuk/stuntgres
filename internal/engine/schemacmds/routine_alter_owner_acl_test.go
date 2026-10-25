package schemacmds_test

import (
	"context"
	"reflect"
	"testing"

	"stuntgres/internal/engine"
	"stuntgres/internal/engine/authorization"
	"stuntgres/internal/engine/enginetest"
	"stuntgres/internal/engine/ids"
)

// Exercise the actual staged ALTER path, not another direct test of the shared
// SchemaACLOwner helper. The public scenario proves execution/default semantics;
// this checks exact grantor/grantee tuples absent from native pg_proc projection.
func TestRoutineAlterOwnerACLTransfer(t *testing.T) {
	for _, kind := range []string{"explicit", "null", "empty"} {
		t.Run(kind, func(t *testing.T) {
			e := engine.New()
			for _, q := range []string{
				"CREATE ROLE routine_acl_next", "CREATE ROLE routine_acl_reader",
				"GRANT CREATE ON SCHEMA public TO routine_acl_next",
				"CREATE FUNCTION routine_acl_owned() RETURNS integer RETURN 1",
			} {
				enginetest.Run(t, e, q)
			}
			next, _ := e.Roles().Lookup("routine_acl_next")
			reader, _ := e.Roles().Lookup("routine_acl_reader")
			view := e.CatalogRead()
			addresses := view.State().ProceduresNamed(ids.ObjectAddress{ClassID: ids.NamespaceClassID, ObjectID: ids.PublicNamespaceOID}, "routine_acl_owned")
			if len(addresses) != 1 {
				t.Fatal("missing unique routine fixture", addresses)
			}
			original := view.MustProcedure(addresses[0])
			acl := authorization.ACLValue{Explicit: kind != "null"}
			want := acl
			if kind == "explicit" {
				acl.Entries = []authorization.ACLEntry{
					{Grantor: original.Owner, Grantee: authorization.ACLGrantee{Role: original.Owner}, Privileges: authorization.ExecutePrivilege, GrantOptions: authorization.ExecutePrivilege},
					{Grantor: next.Ref, Grantee: authorization.ACLGrantee{Role: next.Ref}, Privileges: authorization.ExecutePrivilege},
					{Grantor: original.Owner, Grantee: authorization.ACLGrantee{Role: reader.Ref}, Privileges: authorization.ExecutePrivilege},
					{Grantor: reader.Ref, Grantee: authorization.ACLGrantee{Role: original.Owner}, Privileges: authorization.ExecutePrivilege, GrantOptions: authorization.ExecutePrivilege},
					{Grantor: reader.Ref, Grantee: authorization.ACLGrantee{Public: true}, Privileges: authorization.ExecutePrivilege},
				}
				want.Entries = []authorization.ACLEntry{
					{Grantor: next.Ref, Grantee: authorization.ACLGrantee{Role: next.Ref}, Privileges: authorization.ExecutePrivilege, GrantOptions: authorization.ExecutePrivilege},
					{Grantor: next.Ref, Grantee: authorization.ACLGrantee{Role: reader.Ref}, Privileges: authorization.ExecutePrivilege},
					{Grantor: reader.Ref, Grantee: authorization.ACLGrantee{Role: next.Ref}, Privileges: authorization.ExecutePrivilege, GrantOptions: authorization.ExecutePrivilege},
					{Grantor: reader.Ref, Grantee: authorization.ACLGrantee{Public: true}, Privileges: authorization.ExecutePrivilege},
				}
			}
			ref := view.Owner().Domain.Ref(view.Epoch(), original.Address())
			if err := enginetest.SetObjectACL(e, context.Background(), ref, acl); err != nil {
				t.Fatal(err)
			}
			retained := e.CatalogRead()
			captured := retained.MustProcedure(original.Address())
			enginetest.Run(t, e, "ALTER FUNCTION routine_acl_owned() OWNER TO routine_acl_next")
			current := e.CatalogRead().MustProcedure(original.Address())
			if current.Owner != next.Ref || !reflect.DeepEqual(current.ACL, want) {
				t.Fatalf("owner=%v ACL=%+v want owner=%v ACL=%+v", current.Owner, current.ACL, next.Ref, want)
			}
			if current == captured || current.Revision() <= captured.Revision() || current.SQL != captured.SQL || current.Signature != captured.Signature {
				t.Fatal("owner transfer lost COW revision or immutable declaration identity")
			}
			if captured.Owner != original.Owner || !reflect.DeepEqual(captured.ACL, acl) || retained.MustProcedure(original.Address()) != captured {
				t.Fatal("owner transfer mutated retained descriptor or ACL tuples")
			}
		})
	}
}
