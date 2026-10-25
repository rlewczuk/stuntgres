package tablecmds_test

import (
	"context"
	"errors"
	"reflect"
	"testing"

	"stuntgres/internal/engine"
	"stuntgres/internal/engine/authorization"
	"stuntgres/internal/engine/enginetest"
	"stuntgres/internal/engine/session"
	"stuntgres/internal/sqlstate"
)

func sequenceOwnerCommand(t *testing.T, e *engine.Engine, op *session.Operation, q string, prepared bool) error {
	t.Helper()
	ss := enginetest.Statement(t, q)
	if !prepared {
		_, err := e.Execute(op, ss, 0, nil)
		return err
	}
	d, err := e.Describe(op, ss)
	if err != nil {
		t.Fatalf("Prepare %s: %v", q, err)
	}
	scope, err := e.StartStatement(op)
	if err != nil {
		t.Fatal(err)
	}
	scope.Expected = &d
	_, err = scope.Execute(ss, 0, nil)
	return scope.Finish(err)
}

func sequenceOwnerDiagnostic(t *testing.T, err error, code, message, detail string) {
	t.Helper()
	var got *sqlstate.Error
	want := &sqlstate.Error{Severity: "ERROR", SeverityUnlocalized: "ERROR", Code: code, Message: message, Detail: detail}
	if !errors.As(err, &got) || !reflect.DeepEqual(got, want) {
		t.Fatalf("got %#v (%v), want %#v", got, err, want)
	}
}

func TestSequenceOwnerIdentityOperands(t *testing.T) {
	for _, prepared := range []bool{false, true} {
		for _, operand := range []string{"CURRENT_ROLE", "CURRENT_USER", "SESSION_USER"} {
			t.Run(operand+map[bool]string{false: "/Direct", true: "/Prepared"}[prepared], func(t *testing.T) {
				e := engine.New()
				op, done := enginetest.RoleOperation(t, e, "postgres")
				defer done()
				name := map[string]string{"CURRENT_ROLE": "current_role", "CURRENT_USER": "current_user", "SESSION_USER": "session_user"}[operand]
				enginetest.RoleExecute(t, e, op, `CREATE USER "`+name+`"`)
				enginetest.RoleExecute(t, e, op, "CREATE SEQUENCE s")
				ref := enginetest.RoleTableRef(t, e, "s")
				for _, target := range []string{`"` + name + `"`, operand} {
					if err := sequenceOwnerCommand(t, e, op, "ALTER SEQUENCE s OWNER TO "+target, prepared); err != nil {
						t.Fatal(target, err)
					}
					want, _ := e.Roles().Lookup(name)
					if target == operand {
						want, _ = e.Roles().Lookup("postgres")
					}
					if got := e.Catalog.State.MustRelation(ref.Address).Owner; got != want.Ref {
						t.Fatalf("%s owner=%v want=%v", target, got, want.Ref)
					}
				}
				enginetest.RoleExecute(t, e, op, "CREATE TABLE t(c INT)")
				enginetest.RoleExecute(t, e, op, "ALTER SEQUENCE s OWNED BY t.c")
				if err := sequenceOwnerCommand(t, e, op, "ALTER SEQUENCE s OWNER TO "+operand, prepared); err != nil {
					t.Fatal("linked same owner", err)
				}
				err := sequenceOwnerCommand(t, e, op, `ALTER SEQUENCE s OWNER TO "`+name+`"`, prepared)
				sequenceOwnerDiagnostic(t, err, "0A000", `cannot change owner of sequence "s"`, `Sequence "s" is linked to table "t".`)
			})
		}
	}
}

func TestSequenceOwnerTransitionAuthority(t *testing.T) {
	for _, prepared := range []bool{false, true} {
		t.Run(map[bool]string{false: "Direct", true: "Prepared"}[prepared], func(t *testing.T) {
			e := engine.New()
			op, done := enginetest.RoleOperation(t, e, "postgres")
			defer done()
			for _, q := range []string{"CREATE USER seq_owner", "CREATE USER seq_target", "CREATE USER seq_reader", "CREATE SCHEMA private", "CREATE SEQUENCE private.s CACHE 7", "ALTER SEQUENCE private.s OWNER TO seq_owner"} {
				enginetest.RoleExecute(t, e, op, q)
			}
			ref := enginetest.RoleTableRef(t, e, "private.s")
			old := e.Catalog.State.MustRelation(ref.Address)
			ns := e.Catalog.Domain.Ref(ref.Epoch, old.Namespace())
			owner, _ := e.Roles().Lookup("seq_owner")
			target, _ := e.Roles().Lookup("seq_target")
			reader, _ := e.Roles().Lookup("seq_reader")
			bootstrap, _ := e.Roles().Lookup("postgres")
			if err := enginetest.SetObjectACL(e, context.Background(), ns, authorization.ACLValue{Explicit: true, Entries: []authorization.ACLEntry{{Grantor: bootstrap.Ref, Grantee: authorization.ACLGrantee{Public: true}, Privileges: authorization.UsagePrivilege}}}); err != nil {
				t.Fatal(err)
			}
			acl := authorization.ACLValue{Explicit: true, Entries: []authorization.ACLEntry{{Grantor: owner.Ref, Grantee: authorization.ACLGrantee{Role: owner.Ref}, Privileges: authorization.UsagePrivilege}, {Grantor: owner.Ref, Grantee: authorization.ACLGrantee{Role: reader.Ref}, Privileges: authorization.SelectPrivilege}}}
			if err := enginetest.SetObjectACL(e, context.Background(), ref, acl); err != nil {
				t.Fatal(err)
			}
			enginetest.RoleExecute(t, e, op, "SET SESSION AUTHORIZATION seq_owner")
			r := enginetest.RoleExecute(t, e, op, "SELECT nextval('private.s')")
			r.Rows.Close()
			before := e.Catalog.State.MustRelation(ref.Address)
			state := *e.Storage.Sequences[ref.Address]
			if err := sequenceOwnerCommand(t, e, op, "ALTER SEQUENCE private.s OWNER TO seq_owner", prepared); err != nil {
				t.Fatal("same-owner without CREATE", err)
			}
			after := e.Catalog.State.MustRelation(ref.Address)
			if after.Owner != owner.Ref || after.SequencePlanStamp != before.SequencePlanStamp || !reflect.DeepEqual(after.Security.ACL, before.Security.ACL) || *e.Storage.Sequences[ref.Address] != state {
				t.Fatal("same-owner changed owner, plan stamp, ACL or generator")
			}
			err := sequenceOwnerCommand(t, e, op, "ALTER SEQUENCE private.s OWNER TO seq_target", prepared)
			sequenceOwnerDiagnostic(t, err, "42501", `must be able to SET ROLE "seq_target"`, "")
			if e.Catalog.State.MustRelation(ref.Address) != after || *e.Storage.Sequences[ref.Address] != state {
				t.Fatal("denied transfer mutated sequence")
			}
			enginetest.RoleExecute(t, e, op, "RESET SESSION AUTHORIZATION")
			if err := sequenceOwnerCommand(t, e, op, "ALTER SEQUENCE private.s OWNER TO seq_target", prepared); err != nil {
				t.Fatal("superuser transfer without destination CREATE", err)
			}
			after = e.Catalog.State.MustRelation(ref.Address)
			wantACL := authorization.ACLValue{Explicit: true, Entries: []authorization.ACLEntry{{Grantor: target.Ref, Grantee: authorization.ACLGrantee{Role: target.Ref}, Privileges: authorization.UsagePrivilege}, {Grantor: target.Ref, Grantee: authorization.ACLGrantee{Role: reader.Ref}, Privileges: authorization.SelectPrivilege}}}
			if after.Owner != target.Ref || after.SequencePlanStamp == before.SequencePlanStamp || !reflect.DeepEqual(after.Security.ACL, wantACL) || *e.Storage.Sequences[ref.Address] != state {
				t.Fatalf("transfer owner/stamp/ACL/state: %#v", after)
			}
			enginetest.RoleExecute(t, e, op, "SET SESSION AUTHORIZATION seq_target")
			r = enginetest.RoleExecute(t, e, op, "SELECT nextval('private.s')")
			if got := enginetest.Tuples(r); got[0][0] != int64(2) {
				t.Fatal("transfer disturbed local cache", got)
			}
			r.Rows.Close()
		})
	}
}

// Keep the superuser regression independent of the no-op regression so a
// failure in one boundary cannot prevent the other from being exercised.
func TestSequenceOwnerSuperuserWithoutDestinationCreate(t *testing.T) {
	for _, prepared := range []bool{false, true} {
		t.Run(map[bool]string{false: "Direct", true: "Prepared"}[prepared], func(t *testing.T) {
			e := engine.New()
			op, done := enginetest.RoleOperation(t, e, "postgres")
			defer done()
			for _, q := range []string{"CREATE USER seq_target", "CREATE SCHEMA private", "CREATE SEQUENCE private.s"} {
				enginetest.RoleExecute(t, e, op, q)
			}
			ref := enginetest.RoleTableRef(t, e, "private.s")
			before := e.Catalog.State.MustRelation(ref.Address)
			ns := e.Catalog.Domain.Ref(ref.Epoch, before.Namespace())
			if err := enginetest.SetObjectACL(e, context.Background(), ns, authorization.ACLValue{Explicit: true}); err != nil {
				t.Fatal(err)
			}
			target, _ := e.Roles().Lookup("seq_target")
			if err := sequenceOwnerCommand(t, e, op, "ALTER SEQUENCE private.s OWNER TO seq_target", prepared); err != nil {
				t.Fatal("superuser transfer without destination CREATE", err)
			}
			after := e.Catalog.State.MustRelation(ref.Address)
			if after.Owner != target.Ref || after.SequencePlanStamp == before.SequencePlanStamp {
				t.Fatal("transfer failed to publish owner and plan stamp")
			}
		})
	}
}
