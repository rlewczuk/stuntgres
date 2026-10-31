package core_test

import (
	"context"
	"testing"

	"stuntgres/internal/engine"
	"stuntgres/internal/engine/authorization"
	"stuntgres/internal/engine/enginetest"
)

func TestUpdateFromStoredInvokerMarksRequireBaseUpdate(t *testing.T) {
	e := engine.New()
	op, done := enginetest.RoleOperation(t, e, "postgres")
	defer done()
	for _, q := range []string{"CREATE USER marked_writer", "CREATE TABLE t(a INT)", "CREATE TABLE s(k INT)", "INSERT INTO t VALUES(1)", "INSERT INTO s VALUES(7)", "CREATE VIEW marked WITH (security_invoker=true) AS SELECT k FROM s FOR UPDATE"} {
		enginetest.RoleExecute(t, e, op, q)
	}
	owner := op.RoleState().CurrentUser
	actor, _ := e.Roles().Lookup("marked_writer")
	grant := func(table string, mask authorization.Privileges) {
		t.Helper()
		ref := enginetest.RoleTableRef(t, e, table)
		if err := enginetest.SetObjectACL(e, context.Background(), ref, authorization.ACLValue{Explicit: true, Entries: []authorization.ACLEntry{{Grantor: owner, Grantee: authorization.ACLGrantee{Role: actor.Ref}, Privileges: mask}}}); err != nil {
			t.Fatal(err)
		}
	}
	grant("t", authorization.UpdatePrivilege)
	grant("s", authorization.SelectPrivilege)
	grant("marked", authorization.SelectPrivilege)
	enginetest.RoleExecute(t, e, op, "SET SESSION AUTHORIZATION marked_writer")
	enginetest.RoleError(t, e, op, "UPDATE t SET a=marked.k FROM marked RETURNING marked.k", "42501", "permission denied for table s", "")
	grant("s", authorization.SelectPrivilege|authorization.UpdatePrivilege)
	result := enginetest.RoleExecute(t, e, op, "UPDATE t SET a=marked.k FROM marked RETURNING marked.k")
	if result.Count != 1 {
		t.Fatal(result)
	}
	result.Rows.Close()
	enginetest.RoleExecute(t, e, op, "SET SESSION AUTHORIZATION postgres")
	result = enginetest.RoleExecute(t, e, op, "SELECT a FROM t")
	rows := enginetest.Tuples(result)
	result.Rows.Close()
	if len(rows) != 1 || rows[0][0] != int64(7) {
		t.Fatal(rows)
	}
	enginetest.LockEmpty(t, e)
}
