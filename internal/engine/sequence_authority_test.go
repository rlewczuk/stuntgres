package engine

import (
	"bytes"
	"context"
	"errors"
	"stuntgres/internal/engine/authorization"
	"stuntgres/internal/sqlstate"
	"testing"
)

func TestSequenceAuthorityRemapAndControlAtomicity(t *testing.T) {
	e := New()
	op, done := roleOperation(t, e, "postgres")
	defer done()
	roleExecute(t, e, op, "CREATE USER seq_owner")
	roleExecute(t, e, op, "CREATE USER seq_reader")
	roleExecute(t, e, op, "SET SESSION AUTHORIZATION seq_owner")
	withPublicSchemaUnitOwner(t, e, "seq_owner", func() {
		roleExecute(t, e, op, "CREATE SEQUENCE s CACHE 7")
	})
	r := roleExecute(t, e, op, "SELECT nextval('s')")
	r.Rows.Close()
	roleExecute(t, e, op, "RESET SESSION AUTHORIZATION")
	var dump bytes.Buffer
	if err := e.Dump(context.Background(), &dump); err != nil {
		t.Fatal(err)
	}
	if err := e.Reset(context.Background()); err != nil {
		t.Fatal(err)
	}
	if err := e.Restore(context.Background(), bytes.NewReader(dump.Bytes())); err != nil {
		t.Fatal(err)
	}
	ref := roleTableRef(t, e, "s")
	owner, _ := e.Roles().Lookup("seq_owner")
	reader, _ := e.Roles().Lookup("seq_reader")
	d := e.catalog.State.MustRelation(ref.Address)
	if d.Owner != owner.Ref {
		t.Fatal("owner remap", d.Owner, owner.Ref)
	}
	roleExecute(t, e, op, "SET SESSION AUTHORIZATION seq_owner")
	r = roleExecute(t, e, op, "SELECT nextval('s')")
	if got := tuples(r); got[0][0] != int64(8) {
		t.Fatal(got)
	}
	r.Rows.Close()
	roleExecute(t, e, op, "RESET SESSION AUTHORIZATION")
	acl := authorization.ACLValue{Explicit: true, Entries: []authorization.ACLEntry{{Grantor: owner.Ref, Grantee: authorization.ACLGrantee{Role: reader.Ref}, Privileges: authorization.UsagePrivilege}}}
	if err := e.SetObjectACL(context.Background(), ref, acl); err != nil {
		t.Fatal(err)
	}
	var rejected bytes.Buffer
	if err := e.Dump(context.Background(), &rejected); err == nil || rejected.Len() != 0 {
		t.Fatal("ACL dump must fail before output", err, rejected.String())
	}
	roleError(t, e, op, "DROP ROLE seq_reader", "2BP01", `role "seq_reader" cannot be dropped because some objects depend on it`, "privileges for sequence s")
	before := e.catalog.State
	roleExecute(t, e, op, "SET SESSION AUTHORIZATION seq_reader")
	roleError(t, e, op, "ALTER SEQUENCE s RESTART 100", "42501", "must be owner of sequence s", "")
	if e.catalog.State != before {
		t.Fatal("unauthorized ALTER published catalog")
	}
	roleExecute(t, e, op, "RESET SESSION AUTHORIZATION")
	if err := e.SetObjectACL(context.Background(), ref, authorization.ACLValue{}); err != nil {
		t.Fatal(err)
	}
	roleExecute(t, e, op, "DROP SEQUENCE s")
	roleExecute(t, e, op, "DROP ROLE seq_owner")
	roleExecute(t, e, op, "CREATE USER seq_owner")
	if err := e.Restore(context.Background(), bytes.NewReader(dump.Bytes())); err != nil {
		t.Fatal(err)
	}
	newOwner, _ := e.Roles().Lookup("seq_owner")
	newRef := roleTableRef(t, e, "s")
	if newOwner.Ref == owner.Ref || e.catalog.State.MustRelation(newRef.Address).Owner != newOwner.Ref {
		t.Fatal("restore reused stale principal identity")
	}
}

func TestSequenceNamespacePrivilegeCheckpoints(t *testing.T) {
	e := New()
	op, done := roleOperation(t, e, "postgres")
	defer done()
	roleExecute(t, e, op, "CREATE USER seq_reader")
	roleExecute(t, e, op, "CREATE SCHEMA private")
	roleExecute(t, e, op, "CREATE SEQUENCE private.s")
	ref := roleTableRef(t, e, "private.s")
	namespace := e.catalog.Domain.Ref(e.catalog.State.Epoch(), e.catalog.State.Get(ref.Address).Namespace())
	bootstrap := e.roles.published.Root.Read(authorization.PrincipalRef{}).LookupRef("postgres")
	if err := e.SetObjectACL(context.Background(), namespace, authorization.ACLValue{Explicit: true}); err != nil {
		t.Fatal(err)
	}
	roleExecute(t, e, op, "SET SESSION AUTHORIZATION seq_reader")
	roleErrorPosition(t, e, op, "SELECT nextval('private.s')", "42501", "permission denied for schema private", "", "16")
	roleError(t, e, op, "CREATE SEQUENCE private.denied", "42501", "permission denied for schema private", "")
	roleError(t, e, op, "CREATE SEQUENCE private.denied INCREMENT 0", "22023", "INCREMENT must not be zero", "")
	roleError(t, e, op, "CREATE SEQUENCE IF NOT EXISTS private.s", "42501", "permission denied for schema private", "")
	roleError(t, e, op, "CREATE SEQUENCE IF NOT EXISTS private.s INCREMENT 0", "42501", "permission denied for schema private", "")
	for _, tc := range []struct{ q, code, message string }{
		{"CREATE SEQUENCE private.denied", "42501", "permission denied for schema private"},
		{"CREATE SEQUENCE private.denied INCREMENT 0", "22023", "INCREMENT must not be zero"},
		{"CREATE SEQUENCE IF NOT EXISTS private.s", "42501", "permission denied for schema private"},
		{"CREATE SEQUENCE IF NOT EXISTS private.s INCREMENT 0", "42501", "permission denied for schema private"},
	} {
		description, err := e.Describe(op, statement(t, tc.q))
		if err != nil {
			t.Fatal("utility namespace/options checked during Prepare", tc.q, err)
		}
		scope, err := e.StartStatement(op)
		if err != nil {
			t.Fatal(err)
		}
		scope.Expected = &description
		_, err = scope.Execute(statement(t, tc.q), 0, nil)
		err = scope.Finish(err)
		var got *sqlstate.Error
		want := sqlstate.Error{Severity: "ERROR", SeverityUnlocalized: "ERROR", Code: tc.code, Message: tc.message}
		if !errors.As(err, &got) || *got != want {
			t.Fatalf("%s: %#v, want %#v", tc.q, got, want)
		}
	}
	_, err := e.Describe(op, statement(t, "SELECT nextval('private.s')"))
	var got *sqlstate.Error
	want := sqlstate.Error{Severity: "ERROR", SeverityUnlocalized: "ERROR", Code: "42501", Message: "permission denied for schema private", Position: "16"}
	if !errors.As(err, &got) || *got != want {
		t.Fatalf("regclass Prepare: %#v, want %#v", got, want)
	}
	if err := e.SetObjectACL(context.Background(), namespace, authorization.ACLValue{Explicit: true, Entries: []authorization.ACLEntry{{Grantor: bootstrap, Grantee: authorization.ACLGrantee{Public: true}, Privileges: authorization.UsagePrivilege}}}); err != nil {
		t.Fatal(err)
	}
	if _, err := e.Describe(op, statement(t, "SELECT nextval('private.s')")); err != nil {
		t.Fatal("sequence ACL checked during Prepare", err)
	}
	roleError(t, e, op, "SELECT nextval('private.s')", "42501", "permission denied for sequence s", "")
	roleExecute(t, e, op, "RESET SESSION AUTHORIZATION")
	r := roleExecute(t, e, op, "SELECT nextval('private.s')")
	if got := tuples(r); got[0][0] != int64(1) {
		t.Fatal("denial consumed sequence", got)
	}
	r.Rows.Close()
}
