package core_test

import (
	"context"
	"errors"
	"testing"

	"stuntgres/internal/engine"
	"stuntgres/internal/engine/access"
	"stuntgres/internal/engine/authorization"
	"stuntgres/internal/engine/core"
	"stuntgres/internal/engine/enginetest"
	"stuntgres/internal/sqlstate"
)

func TestDeleteCoreAuthorization(t *testing.T) {
	e := engine.New()
	op, done := enginetest.RoleOperation(t, e, "postgres")
	defer done()
	enginetest.RoleExecute(t, e, op, "CREATE USER reader")
	enginetest.RoleExecute(t, e, op, "CREATE USER selector")
	enginetest.RoleExecute(t, e, op, "CREATE TABLE t(a INT,b INT)")
	enginetest.RoleExecute(t, e, op, "INSERT INTO t VALUES(1,2),(3,4)")
	ref := enginetest.RoleTableRef(t, e, "t")
	owner := op.RoleState().CurrentUser
	reader, _ := e.Roles().Lookup("reader")
	selector, _ := e.Roles().Lookup("selector")
	grant := func(mask authorization.Privileges) {
		t.Helper()
		acl := authorization.ACLValue{Explicit: true, Entries: []authorization.ACLEntry{
			{Grantor: owner, Grantee: authorization.ACLGrantee{Role: reader.Ref}, Privileges: mask},
			{Grantor: owner, Grantee: authorization.ACLGrantee{Role: selector.Ref}, Privileges: authorization.SelectPrivilege},
		}}
		if err := enginetest.SetObjectACL(e, context.Background(), ref, acl); err != nil {
			t.Fatal(err)
		}
	}
	grant(authorization.DeletePrivilege)
	enginetest.RoleExecute(t, e, op, "SET SESSION AUTHORIZATION reader")
	for _, query := range []string{"DELETE FROM t WHERE FALSE", "DELETE FROM t WHERE NULL"} {
		enginetest.RoleExecute(t, e, op, query)
	}
	ast := enginetest.Statement(t, "DELETE FROM t AS d WHERE d.a=1 AND d.a=1")
	prepared, err := e.Describe(op, ast)
	if err != nil {
		t.Fatal("ordinary DELETE ACL rejected at Prepare", err)
	}
	enginetest.RoleError(t, e, op, "DELETE FROM t WHERE a=1", "42501", "permission denied for table t", "")
	column := ref
	column.Address.SubID = 1
	if err := enginetest.SetObjectACL(e, context.Background(), column, authorization.ACLValue{Explicit: true, Entries: []authorization.ACLEntry{{Grantor: owner, Grantee: authorization.ACLGrantee{Role: reader.Ref}, Privileges: authorization.DeletePrivilege}}}); err == nil {
		t.Fatal("column DELETE accepted")
	}
	if err := enginetest.SetObjectACL(e, context.Background(), column, authorization.ACLValue{Explicit: true, Entries: []authorization.ACLEntry{{Grantor: owner, Grantee: authorization.ACLGrantee{Role: reader.Ref}, Privileges: authorization.SelectPrivilege}}}); err != nil {
		t.Fatal(err)
	}
	scope, err := e.StartStatement(op)
	if err != nil {
		t.Fatal(err)
	}
	scope.Expected = &prepared
	result, err := scope.Execute(ast, 0, nil)
	err = scope.Finish(err)
	if err != nil || result.Count != 1 || result.Rows != nil {
		t.Fatal(result, err)
	}
	enginetest.RoleError(t, e, op, "DELETE FROM t WHERE b=4", "42501", "permission denied for table t", "")
	enginetest.RoleExecute(t, e, op, "SET SESSION AUTHORIZATION selector")
	enginetest.RoleError(t, e, op, "DELETE FROM t", "42501", "permission denied for table t", "")
	enginetest.RoleExecute(t, e, op, "SET SESSION AUTHORIZATION reader")
	enginetest.RoleExecute(t, e, op, "DELETE FROM t") // no fabricated SELECT requirement
	grant(authorization.DeletePrivilege | authorization.SelectPrivilege)
	ast = enginetest.Statement(t, "DELETE FROM t WHERE a=1")
	prepared, err = e.Describe(op, ast)
	if err != nil {
		t.Fatal(err)
	}
	grant(authorization.SelectPrivilege) // revocation must invalidate allowance, not the handle
	scope, err = e.StartStatement(op)
	if err != nil {
		t.Fatal(err)
	}
	scope.Expected = &prepared
	result, err = scope.Execute(ast, 0, nil)
	err = scope.Finish(err)
	want := sqlstate.Error{Severity: "ERROR", SeverityUnlocalized: "ERROR", Code: "42501", Message: "permission denied for table t"}
	var diagnostic *sqlstate.Error
	if result.Count != 0 || result.Rows != nil || !errors.As(err, &diagnostic) || *diagnostic != want {
		t.Fatal(result, err, diagnostic)
	}
	view, err := core.OperationAuthorization(op)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := view.Effective(column, false); err != nil {
		t.Fatal(err)
	}
	if err := view.Check(access.Requirement{Object: column, Privileges: authorization.DeletePrivilege}); err == nil {
		t.Fatal("column DELETE requirement accepted")
	}
	enginetest.LockEmpty(t, e)
}
