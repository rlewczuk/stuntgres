package engine

import (
	"context"
	"stuntgres/internal/engine/authorization"
	"testing"
)

func TestDeleteReturningAuthorization(t *testing.T) {
	e := New()
	op, done := roleOperation(t, e, "postgres")
	defer done()
	for _, q := range []string{"CREATE USER returning_reader", "CREATE TABLE t(id INT,v TEXT)", "INSERT INTO t VALUES(1,'a'),(2,'b')"} {
		roleExecute(t, e, op, q)
	}
	ref := roleTableRef(t, e, "t")
	owner := op.RoleState().CurrentUser
	reader, _ := e.Roles().Lookup("returning_reader")
	grant := func(target ObjectRef, mask authorization.Privileges) {
		t.Helper()
		acl := authorization.ACLValue{Explicit: true, Entries: []authorization.ACLEntry{{Grantor: owner, Grantee: authorization.ACLGrantee{Role: reader.Ref}, Privileges: mask}}}
		if err := e.SetObjectACL(context.Background(), target, acl); err != nil {
			t.Fatal(err)
		}
	}
	grant(ref, authorization.DeletePrivilege)
	roleExecute(t, e, op, "SET SESSION AUTHORIZATION returning_reader")
	constant := roleExecute(t, e, op, "DELETE FROM t WHERE FALSE RETURNING 1")
	if constant.Rows != nil {
		constant.Rows.Close()
	}
	for _, q := range []string{"DELETE FROM t RETURNING id", "DELETE FROM t RETURNING old.id", "DELETE FROM t RETURNING new.id", "DELETE FROM t RETURNING WITH (NEW AS n) n.id", "DELETE FROM t RETURNING CASE WHEN FALSE THEN v ELSE 'safe' END"} {
		if _, err := e.Describe(op, statement(t, q)); err != nil {
			t.Fatal("Prepare ACL", err)
		}
		roleError(t, e, op, q, "42501", "permission denied for table t", "")
	}
	column := ref
	column.Address.SubID = 1
	grant(column, authorization.SelectPrivilege)
	for _, q := range []string{"DELETE FROM t WHERE FALSE RETURNING id", "DELETE FROM t WHERE FALSE RETURNING old.id", "DELETE FROM t WHERE FALSE RETURNING new.id"} {
		r := roleExecute(t, e, op, q)
		r.Rows.Close()
	}
	for _, q := range []string{"DELETE FROM t RETURNING v", "DELETE FROM t RETURNING *", "DELETE FROM t RETURNING old.*", "DELETE FROM t RETURNING new.*"} {
		roleError(t, e, op, q, "42501", "permission denied for table t", "")
	}
	ast := statement(t, "DELETE FROM t RETURNING new.id")
	description, err := e.Describe(op, ast)
	if err != nil {
		t.Fatal(err)
	}
	// Held execution genuinely succeeds before its required privilege is revoked.
	stage, err := e.StartStatement(op)
	if err != nil {
		t.Fatal(err)
	}
	stage.Expected = &description
	result, err := stage.Execute(ast, 0, nil)
	err = stage.Finish(err)
	if err != nil || result.Count != 2 {
		t.Fatal(result, err)
	}
	result.Rows.Close()
	roleExecute(t, e, op, "SET SESSION AUTHORIZATION postgres")
	roleExecute(t, e, op, "INSERT INTO t VALUES(3,'c')")
	grant(column, 0)
	roleExecute(t, e, op, "SET SESSION AUTHORIZATION returning_reader")
	stage, err = e.StartStatement(op)
	if err != nil {
		t.Fatal(err)
	}
	stage.Expected = &description
	result, err = stage.Execute(ast, 0, nil)
	err = stage.Finish(err)
	state(t, err, "42501")
	if result.Rows != nil || result.Count != 0 {
		t.Fatal(result)
	}
	roleExecute(t, e, op, "SET SESSION AUTHORIZATION postgres")
	r := roleExecute(t, e, op, "SELECT id FROM t")
	if r.Count != 1 {
		t.Fatal(r)
	}
	r.Rows.Close()
	roleExecute(t, e, op, "SET SESSION AUTHORIZATION returning_reader")
	roleExecute(t, e, op, "BEGIN")
	r = roleExecute(t, e, op, "DELETE FROM t RETURNING 1")
	if r.Count != 1 {
		t.Fatal(r)
	}
	r.Rows.Close()
	grant(ref, 0) // ACL revision after successful staging is not a COMMIT veto.
	roleExecute(t, e, op, "COMMIT")
	roleExecute(t, e, op, "SET SESSION AUTHORIZATION postgres")
	r = roleExecute(t, e, op, "SELECT id FROM t")
	if r.Count != 0 {
		t.Fatal(r)
	}
	r.Rows.Close()
	lockEmpty(t, e)
}
