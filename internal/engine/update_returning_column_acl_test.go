package engine

import (
	"context"
	"stuntgres/internal/engine/authorization"
	"testing"
)

func TestUpdateReturningColumnAuthorization(t *testing.T) {
	e := New()
	op, done := roleOperation(t, e, "postgres")
	defer done()
	for _, q := range []string{"CREATE USER output_writer", "CREATE TABLE t(a INT,v TEXT)", "INSERT INTO t VALUES(1,'carried')"} {
		roleExecute(t, e, op, q)
	}
	owner := op.RoleState().CurrentUser
	reader, _ := e.Roles().Lookup("output_writer")
	ref := roleTableRef(t, e, "t")
	column := ref
	column.Address.SubID = 1
	grant := func(mask authorization.Privileges) {
		t.Helper()
		if err := e.SetObjectACL(context.Background(), column, authorization.ACLValue{Explicit: true, Entries: []authorization.ACLEntry{{Grantor: owner, Grantee: authorization.ACLGrantee{Role: reader.Ref}, Privileges: mask}}}); err != nil {
			t.Fatal(err)
		}
	}
	grant(authorization.UpdatePrivilege)
	roleExecute(t, e, op, "SET SESSION AUTHORIZATION output_writer")
	r := roleExecute(t, e, op, "UPDATE t SET a=7 RETURNING 1")
	if r.Count != 1 {
		t.Fatal(r)
	}
	r.Rows.Close()
	for _, q := range []string{"UPDATE t SET a=8 RETURNING a", "UPDATE t SET a=8 WHERE FALSE RETURNING old.a", "UPDATE t SET a=8 RETURNING new.a", "UPDATE t SET a=8 RETURNING CASE WHEN FALSE THEN v ELSE 'safe' END"} {
		roleError(t, e, op, q, "42501", "permission denied for table t", "")
	}
	grant(authorization.UpdatePrivilege | authorization.SelectPrivilege)
	r = roleExecute(t, e, op, "UPDATE t SET a=8 RETURNING old.a,new.a")
	got := tuples(r)
	r.Rows.Close()
	if len(got) != 1 || got[0][0] != int64(7) || got[0][1] != int64(8) {
		t.Fatal(got)
	}
	for _, q := range []string{"UPDATE t SET a=9 RETURNING v", "UPDATE t SET a=9 RETURNING *"} {
		roleError(t, e, op, q, "42501", "permission denied for table t", "")
	}
	roleExecute(t, e, op, "SET SESSION AUTHORIZATION postgres")
	r = roleExecute(t, e, op, "SELECT a,v FROM t")
	got = tuples(r)
	r.Rows.Close()
	if len(got) != 1 || got[0][0] != int64(8) || got[0][1] != "carried" {
		t.Fatal(got)
	}
	lockEmpty(t, e)
}
