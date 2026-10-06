package engine

import (
	"context"
	"errors"
	"reflect"
	"stuntgres/internal/sqlstate"
	"testing"
)

func sequenceOwnerCommand(t *testing.T, e *Engine, op *Operation, q string, prepared bool) error {
	t.Helper()
	ss := statement(t, q)
	if !prepared {
		_, err := e.Execute(op, ss, 0, nil)
		return err
	}
	d, err := e.Describe(op, ss)
	if err != nil { t.Fatalf("Prepare %s: %v", q, err) }
	scope, err := e.StartStatement(op)
	if err != nil { t.Fatal(err) }
	scope.Expected = &d
	_, err = scope.Execute(ss, 0, nil)
	return scope.Finish(err)
}

func sequenceOwnerDiagnostic(t *testing.T, err error, code, message, detail string) {
	t.Helper()
	var got *sqlstate.Error
	want := &sqlstate.Error{Severity: "ERROR", SeverityUnlocalized: "ERROR", Code: code, Message: message, Detail: detail}
	if !errors.As(err, &got) || !reflect.DeepEqual(got, want) { t.Fatalf("got %#v (%v), want %#v", got, err, want) }
}

func TestSequenceOwnerIdentityOperands(t *testing.T) {
	for _, prepared := range []bool{false, true} {
		for _, operand := range []string{"CURRENT_ROLE", "CURRENT_USER", "SESSION_USER"} {
			t.Run(operand+map[bool]string{false:"/Direct",true:"/Prepared"}[prepared], func(t *testing.T) {
				e := New()
				op, done := roleOperation(t, e, "postgres"); defer done()
				name := map[string]string{"CURRENT_ROLE":"current_role","CURRENT_USER":"current_user","SESSION_USER":"session_user"}[operand]
				roleExecute(t,e,op,`CREATE USER "`+name+`"`)
				roleExecute(t,e,op,"CREATE SEQUENCE s")
				ref := roleTableRef(t,e,"s")
				for _, target := range []string{`"`+name+`"`, operand} {
					if err := sequenceOwnerCommand(t,e,op,"ALTER SEQUENCE s OWNER TO "+target,prepared); err != nil { t.Fatal(target,err) }
					want, _ := e.Roles().Lookup(name)
					if target == operand { want, _ = e.Roles().Lookup("postgres") }
					if got := e.state.descriptors[ref.Address].(*relationDescriptor).owner; got != want.Ref { t.Fatalf("%s owner=%v want=%v", target,got,want.Ref) }
				}
				roleExecute(t,e,op,"CREATE TABLE t(c INT)")
				roleExecute(t,e,op,"ALTER SEQUENCE s OWNED BY t.c")
				if err := sequenceOwnerCommand(t,e,op,"ALTER SEQUENCE s OWNER TO "+operand,prepared); err != nil { t.Fatal("linked same owner",err) }
				err := sequenceOwnerCommand(t,e,op,`ALTER SEQUENCE s OWNER TO "`+name+`"`,prepared)
				sequenceOwnerDiagnostic(t,err,"0A000",`cannot change owner of sequence "s"`,`Sequence "s" is linked to table "t".`)
			})
		}
	}
}

func TestSequenceOwnerTransitionAuthority(t *testing.T) {
	for _, prepared := range []bool{false,true} {
		t.Run(map[bool]string{false:"Direct",true:"Prepared"}[prepared],func(t *testing.T) {
			e := New(); op, done := roleOperation(t,e,"postgres"); defer done()
			for _, q := range []string{"CREATE USER seq_owner", "CREATE USER seq_target", "CREATE USER seq_reader", "CREATE SCHEMA private", "CREATE SEQUENCE private.s CACHE 7", "ALTER SEQUENCE private.s OWNER TO seq_owner"} { roleExecute(t,e,op,q) }
			ref := roleTableRef(t,e,"private.s")
			old := e.state.descriptors[ref.Address].(*relationDescriptor)
			ns := ObjectRef{owner:e.owner,Epoch:ref.Epoch,Address:old.namespace}
			owner,_ := e.Roles().Lookup("seq_owner"); target,_ := e.Roles().Lookup("seq_target"); reader,_ := e.Roles().Lookup("seq_reader")
			bootstrap,_ := e.Roles().Lookup("postgres")
			if err := e.SetObjectACL(context.Background(),ns,ACLValue{Explicit:true,Entries:[]ACLEntry{{Grantor:bootstrap.Ref,Grantee:ACLGrantee{Public:true},Privileges:UsagePrivilege}}}); err != nil { t.Fatal(err) }
			acl := ACLValue{Explicit:true,Entries:[]ACLEntry{{Grantor:owner.Ref,Grantee:ACLGrantee{Role:owner.Ref},Privileges:UsagePrivilege},{Grantor:owner.Ref,Grantee:ACLGrantee{Role:reader.Ref},Privileges:SelectPrivilege}}}
			if err := e.SetObjectACL(context.Background(),ref,acl); err != nil { t.Fatal(err) }
			roleExecute(t,e,op,"SET SESSION AUTHORIZATION seq_owner")
			r := roleExecute(t,e,op,"SELECT nextval('private.s')"); r.Rows.Close()
			before := e.state.descriptors[ref.Address].(*relationDescriptor)
			state := *e.sequences[ref.Address]
			if err := sequenceOwnerCommand(t,e,op,"ALTER SEQUENCE private.s OWNER TO seq_owner",prepared); err != nil { t.Fatal("same-owner without CREATE",err) }
			after := e.state.descriptors[ref.Address].(*relationDescriptor)
			if after.owner != owner.Ref || after.sequencePlanStamp != before.sequencePlanStamp || !reflect.DeepEqual(after.acl,before.acl) || *e.sequences[ref.Address] != state { t.Fatal("same-owner changed owner, plan stamp, ACL or generator") }
			err := sequenceOwnerCommand(t,e,op,"ALTER SEQUENCE private.s OWNER TO seq_target",prepared)
			sequenceOwnerDiagnostic(t,err,"42501",`must be able to SET ROLE "seq_target"`,"")
			if e.state.descriptors[ref.Address].(*relationDescriptor) != after || *e.sequences[ref.Address] != state { t.Fatal("denied transfer mutated sequence") }
			roleExecute(t,e,op,"RESET SESSION AUTHORIZATION")
			if err := sequenceOwnerCommand(t,e,op,"ALTER SEQUENCE private.s OWNER TO seq_target",prepared); err != nil { t.Fatal("superuser transfer without destination CREATE",err) }
			after = e.state.descriptors[ref.Address].(*relationDescriptor)
			wantACL := ACLValue{Explicit:true,Entries:[]ACLEntry{{Grantor:target.Ref,Grantee:ACLGrantee{Role:target.Ref},Privileges:UsagePrivilege},{Grantor:target.Ref,Grantee:ACLGrantee{Role:reader.Ref},Privileges:SelectPrivilege}}}
			if after.owner != target.Ref || after.sequencePlanStamp == before.sequencePlanStamp || !reflect.DeepEqual(after.acl,wantACL) || *e.sequences[ref.Address] != state { t.Fatalf("transfer owner/stamp/ACL/state: %#v",after) }
			roleExecute(t,e,op,"SET SESSION AUTHORIZATION seq_target")
			r = roleExecute(t,e,op,"SELECT nextval('private.s')")
			if got := tuples(r); got[0][0] != int64(2) { t.Fatal("transfer disturbed local cache",got) }; r.Rows.Close()
		})
	}
}

// Keep the superuser regression independent of the no-op regression so a
// failure in one boundary cannot prevent the other from being exercised.
func TestSequenceOwnerSuperuserWithoutDestinationCreate(t *testing.T) {
	for _, prepared := range []bool{false, true} {
		t.Run(map[bool]string{false: "Direct", true: "Prepared"}[prepared], func(t *testing.T) {
			e := New()
			op, done := roleOperation(t, e, "postgres")
			defer done()
			for _, q := range []string{"CREATE USER seq_target", "CREATE SCHEMA private", "CREATE SEQUENCE private.s"} {
				roleExecute(t, e, op, q)
			}
			ref := roleTableRef(t, e, "private.s")
			before := e.state.descriptors[ref.Address].(*relationDescriptor)
			ns := ObjectRef{owner: e.owner, Epoch: ref.Epoch, Address: before.namespace}
			if err := e.SetObjectACL(context.Background(), ns, ACLValue{Explicit: true}); err != nil {
				t.Fatal(err)
			}
			target, _ := e.Roles().Lookup("seq_target")
			if err := sequenceOwnerCommand(t, e, op, "ALTER SEQUENCE private.s OWNER TO seq_target", prepared); err != nil {
				t.Fatal("superuser transfer without destination CREATE", err)
			}
			after := e.state.descriptors[ref.Address].(*relationDescriptor)
			if after.owner != target.Ref || after.sequencePlanStamp == before.sequencePlanStamp {
				t.Fatal("transfer failed to publish owner and plan stamp")
			}
		})
	}
}
