package tablecmds_test

import (
	"bytes"
	"context"
	"errors"
	"reflect"
	"testing"

	"stuntgres/internal/engine"
	"stuntgres/internal/engine/authorization"
	"stuntgres/internal/engine/enginetest"
	"stuntgres/internal/sqlstate"
)

func TestSequenceAuthorityRemapAndControlAtomicity(t *testing.T) {
	e := engine.New()
	op, done := enginetest.RoleOperation(t, e, "postgres")
	defer done()
	enginetest.RoleExecute(t, e, op, "CREATE USER seq_owner")
	enginetest.RoleExecute(t, e, op, "CREATE USER seq_reader")
	enginetest.RoleExecute(t, e, op, "SET SESSION AUTHORIZATION seq_owner")
	withPublicSchemaUnitOwner(t, e, "seq_owner", func() {
		enginetest.RoleExecute(t, e, op, "CREATE SEQUENCE s CACHE 7")
	})
	r := enginetest.RoleExecute(t, e, op, "SELECT nextval('s')")
	r.Rows.Close()
	enginetest.RoleExecute(t, e, op, "RESET SESSION AUTHORIZATION")
	var dump bytes.Buffer
	if err := engine.Dump(context.Background(), e, &dump); err != nil {
		t.Fatal(err)
	}
	if err := engine.Reset(context.Background(), e); err != nil {
		t.Fatal(err)
	}
	if err := engine.Restore(context.Background(), e, bytes.NewReader(dump.Bytes())); err != nil {
		t.Fatal(err)
	}
	ref := enginetest.RoleTableRef(t, e, "s")
	owner, _ := e.Roles().Lookup("seq_owner")
	reader, _ := e.Roles().Lookup("seq_reader")
	d := e.Catalog.State.MustRelation(ref.Address)
	if d.Owner != owner.Ref {
		t.Fatal("owner remap", d.Owner, owner.Ref)
	}
	enginetest.RoleExecute(t, e, op, "SET SESSION AUTHORIZATION seq_owner")
	r = enginetest.RoleExecute(t, e, op, "SELECT nextval('s')")
	if got := enginetest.Tuples(r); got[0][0] != int64(8) {
		t.Fatal(got)
	}
	r.Rows.Close()
	enginetest.RoleExecute(t, e, op, "RESET SESSION AUTHORIZATION")
	acl := authorization.ACLValue{Explicit: true, Entries: []authorization.ACLEntry{{Grantor: owner.Ref, Grantee: authorization.ACLGrantee{Role: reader.Ref}, Privileges: authorization.UsagePrivilege}}}
	if err := enginetest.SetObjectACL(e, context.Background(), ref, acl); err != nil {
		t.Fatal(err)
	}
	var granted bytes.Buffer
	if err := engine.Dump(context.Background(), e, &granted); err != nil {
		t.Fatal(err)
	}
	if err := engine.Restore(context.Background(), e, bytes.NewReader(granted.Bytes())); err != nil {
		t.Fatal(err)
	}
	ref = enginetest.RoleTableRef(t, e, "s")
	if got := e.Catalog.State.MustRelation(ref.Address).Security.ACL; !reflect.DeepEqual(got, acl) {
		t.Fatal("sequence ACL changed across restore", got, acl)
	}
	enginetest.RoleError(t, e, op, "DROP ROLE seq_reader", "2BP01", `role "seq_reader" cannot be dropped because some objects depend on it`, "privileges for sequence s")
	before := e.Catalog.State
	enginetest.RoleExecute(t, e, op, "SET SESSION AUTHORIZATION seq_reader")
	enginetest.RoleError(t, e, op, "ALTER SEQUENCE s RESTART 100", "42501", "must be owner of sequence s", "")
	if e.Catalog.State != before {
		t.Fatal("unauthorized ALTER published catalog")
	}
	enginetest.RoleExecute(t, e, op, "RESET SESSION AUTHORIZATION")
	if err := enginetest.SetObjectACL(e, context.Background(), ref, authorization.ACLValue{}); err != nil {
		t.Fatal(err)
	}
	enginetest.RoleExecute(t, e, op, "DROP SEQUENCE s")
	enginetest.RoleExecute(t, e, op, "DROP ROLE seq_owner")
	enginetest.RoleExecute(t, e, op, "CREATE USER seq_owner")
	if err := engine.Restore(context.Background(), e, bytes.NewReader(dump.Bytes())); err != nil {
		t.Fatal(err)
	}
	newOwner, _ := e.Roles().Lookup("seq_owner")
	newRef := enginetest.RoleTableRef(t, e, "s")
	if newOwner.Ref == owner.Ref || e.Catalog.State.MustRelation(newRef.Address).Owner != newOwner.Ref {
		t.Fatal("restore reused stale principal identity")
	}
}

func TestSequenceNamespacePrivilegeCheckpoints(t *testing.T) {
	e := engine.New()
	op, done := enginetest.RoleOperation(t, e, "postgres")
	defer done()
	enginetest.RoleExecute(t, e, op, "CREATE USER seq_reader")
	enginetest.RoleExecute(t, e, op, "CREATE SCHEMA private")
	enginetest.RoleExecute(t, e, op, "CREATE SEQUENCE private.s")
	ref := enginetest.RoleTableRef(t, e, "private.s")
	namespace := e.Catalog.Domain.Ref(e.Catalog.State.Epoch(), e.Catalog.State.Get(ref.Address).Namespace())
	bootstrap := e.RoleStore.Published.Root.Read(authorization.PrincipalRef{}).LookupRef("postgres")
	if err := enginetest.SetObjectACL(e, context.Background(), namespace, authorization.ACLValue{Explicit: true}); err != nil {
		t.Fatal(err)
	}
	enginetest.RoleExecute(t, e, op, "SET SESSION AUTHORIZATION seq_reader")
	enginetest.RoleErrorPosition(t, e, op, "SELECT nextval('private.s')", "42501", "permission denied for schema private", "", "16")
	enginetest.RoleError(t, e, op, "CREATE SEQUENCE private.denied", "42501", "permission denied for schema private", "")
	enginetest.RoleError(t, e, op, "CREATE SEQUENCE private.denied INCREMENT 0", "22023", "INCREMENT must not be zero", "")
	enginetest.RoleError(t, e, op, "CREATE SEQUENCE IF NOT EXISTS private.s", "42501", "permission denied for schema private", "")
	enginetest.RoleError(t, e, op, "CREATE SEQUENCE IF NOT EXISTS private.s INCREMENT 0", "42501", "permission denied for schema private", "")
	for _, tc := range []struct{ q, code, message string }{
		{"CREATE SEQUENCE private.denied", "42501", "permission denied for schema private"},
		{"CREATE SEQUENCE private.denied INCREMENT 0", "22023", "INCREMENT must not be zero"},
		{"CREATE SEQUENCE IF NOT EXISTS private.s", "42501", "permission denied for schema private"},
		{"CREATE SEQUENCE IF NOT EXISTS private.s INCREMENT 0", "42501", "permission denied for schema private"},
	} {
		description, err := e.Describe(op, enginetest.Statement(t, tc.q))
		if err != nil {
			t.Fatal("utility namespace/options checked during Prepare", tc.q, err)
		}
		scope, err := e.StartStatement(op)
		if err != nil {
			t.Fatal(err)
		}
		scope.Expected = &description
		_, err = scope.Execute(enginetest.Statement(t, tc.q), 0, nil)
		err = scope.Finish(err)
		var got *sqlstate.Error
		want := sqlstate.Error{Severity: "ERROR", SeverityUnlocalized: "ERROR", Code: tc.code, Message: tc.message}
		if !errors.As(err, &got) || *got != want {
			t.Fatalf("%s: %#v, want %#v", tc.q, got, want)
		}
	}
	_, err := e.Describe(op, enginetest.Statement(t, "SELECT nextval('private.s')"))
	var got *sqlstate.Error
	want := sqlstate.Error{Severity: "ERROR", SeverityUnlocalized: "ERROR", Code: "42501", Message: "permission denied for schema private", Position: "16"}
	if !errors.As(err, &got) || *got != want {
		t.Fatalf("regclass Prepare: %#v, want %#v", got, want)
	}
	if err := enginetest.SetObjectACL(e, context.Background(), namespace, authorization.ACLValue{Explicit: true, Entries: []authorization.ACLEntry{{Grantor: bootstrap, Grantee: authorization.ACLGrantee{Public: true}, Privileges: authorization.UsagePrivilege}}}); err != nil {
		t.Fatal(err)
	}
	if _, err := e.Describe(op, enginetest.Statement(t, "SELECT nextval('private.s')")); err != nil {
		t.Fatal("sequence ACL checked during Prepare", err)
	}
	enginetest.RoleError(t, e, op, "SELECT nextval('private.s')", "42501", "permission denied for sequence s", "")
	enginetest.RoleExecute(t, e, op, "RESET SESSION AUTHORIZATION")
	r := enginetest.RoleExecute(t, e, op, "SELECT nextval('private.s')")
	if got := enginetest.Tuples(r); got[0][0] != int64(1) {
		t.Fatal("denial consumed sequence", got)
	}
	r.Rows.Close()
}
