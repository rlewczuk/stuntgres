package core_test

import (
	"context"
	"testing"

	"stuntgres/internal/engine"
	"stuntgres/internal/engine/authorization"
	"stuntgres/internal/engine/enginetest"
	"stuntgres/internal/engine/ids"
)

func TestDeleteReturningAuthorization(t *testing.T) {
	e := engine.New()
	op, done := enginetest.RoleOperation(t, e, "postgres")
	defer done()
	for _, q := range []string{"CREATE USER returning_reader", "CREATE TABLE t(id INT,v TEXT)", "INSERT INTO t VALUES(1,'a'),(2,'b')"} {
		enginetest.RoleExecute(t, e, op, q)
	}
	ref := enginetest.RoleTableRef(t, e, "t")
	owner := op.RoleState().CurrentUser
	reader, _ := e.Roles().Lookup("returning_reader")
	grant := func(target ids.ObjectRef, mask authorization.Privileges) {
		t.Helper()
		acl := authorization.ACLValue{Explicit: true, Entries: []authorization.ACLEntry{{Grantor: owner, Grantee: authorization.ACLGrantee{Role: reader.Ref}, Privileges: mask}}}
		if err := enginetest.SetObjectACL(e, context.Background(), target, acl); err != nil {
			t.Fatal(err)
		}
	}
	grant(ref, authorization.DeletePrivilege)
	enginetest.RoleExecute(t, e, op, "SET SESSION AUTHORIZATION returning_reader")
	constant := enginetest.RoleExecute(t, e, op, "DELETE FROM t WHERE FALSE RETURNING 1")
	if constant.Rows != nil {
		constant.Rows.Close()
	}
	for _, q := range []string{"DELETE FROM t RETURNING id", "DELETE FROM t RETURNING old.id", "DELETE FROM t RETURNING new.id", "DELETE FROM t RETURNING WITH (NEW AS n) n.id", "DELETE FROM t RETURNING CASE WHEN FALSE THEN v ELSE 'safe' END"} {
		if _, err := e.Describe(op, enginetest.Statement(t, q)); err != nil {
			t.Fatal("Prepare ACL", err)
		}
		enginetest.RoleError(t, e, op, q, "42501", "permission denied for table t", "")
	}
	column := ref
	column.Address.SubID = 1
	grant(column, authorization.SelectPrivilege)
	for _, q := range []string{"DELETE FROM t WHERE FALSE RETURNING id", "DELETE FROM t WHERE FALSE RETURNING old.id", "DELETE FROM t WHERE FALSE RETURNING new.id"} {
		r := enginetest.RoleExecute(t, e, op, q)
		r.Rows.Close()
	}
	for _, q := range []string{"DELETE FROM t RETURNING v", "DELETE FROM t RETURNING *", "DELETE FROM t RETURNING old.*", "DELETE FROM t RETURNING new.*"} {
		enginetest.RoleError(t, e, op, q, "42501", "permission denied for table t", "")
	}
	ast := enginetest.Statement(t, "DELETE FROM t RETURNING new.id")
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
	enginetest.RoleExecute(t, e, op, "SET SESSION AUTHORIZATION postgres")
	enginetest.RoleExecute(t, e, op, "INSERT INTO t VALUES(3,'c')")
	grant(column, 0)
	enginetest.RoleExecute(t, e, op, "SET SESSION AUTHORIZATION returning_reader")
	stage, err = e.StartStatement(op)
	if err != nil {
		t.Fatal(err)
	}
	stage.Expected = &description
	result, err = stage.Execute(ast, 0, nil)
	err = stage.Finish(err)
	enginetest.State(t, err, "42501")
	if result.Rows != nil || result.Count != 0 {
		t.Fatal(result)
	}
	enginetest.RoleExecute(t, e, op, "SET SESSION AUTHORIZATION postgres")
	r := enginetest.RoleExecute(t, e, op, "SELECT id FROM t")
	if r.Count != 1 {
		t.Fatal(r)
	}
	r.Rows.Close()
	enginetest.RoleExecute(t, e, op, "SET SESSION AUTHORIZATION returning_reader")
	enginetest.RoleExecute(t, e, op, "BEGIN")
	r = enginetest.RoleExecute(t, e, op, "DELETE FROM t RETURNING 1")
	if r.Count != 1 {
		t.Fatal(r)
	}
	r.Rows.Close()
	grant(ref, 0) // ACL revision after successful staging is not a COMMIT veto.
	enginetest.RoleExecute(t, e, op, "COMMIT")
	enginetest.RoleExecute(t, e, op, "SET SESSION AUTHORIZATION postgres")
	r = enginetest.RoleExecute(t, e, op, "SELECT id FROM t")
	if r.Count != 0 {
		t.Fatal(r)
	}
	r.Rows.Close()
	enginetest.LockEmpty(t, e)
}
