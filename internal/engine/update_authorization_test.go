package engine

import (
	"context"
	"database/sql/driver"
	"reflect"
	"testing"
)

func TestUpdateCoreColumnAuthority(t *testing.T) {
	e := New()
	op, done := roleOperation(t, e, "postgres")
	defer done()
	roleExecute(t, e, op, "CREATE ROLE update_reader")
	roleExecute(t, e, op, "CREATE TABLE t(a INT,b INT,c INT)")
	roleExecute(t, e, op, "INSERT INTO t VALUES(1,2,3)")
	ref := roleTableRef(t, e, "t")
	role, _ := e.Roles().Lookup("update_reader")
	column := ref
	column.Address.SubID = 1
	grant := func(ref ObjectRef, privileges Privileges) {
		t.Helper()
		err := e.SetObjectACL(context.Background(), ref, ACLValue{Explicit: true, Entries: []ACLEntry{{Grantor: e.roles.names["postgres"], Grantee: ACLGrantee{Role: role.Ref}, Privileges: privileges}}})
		if err != nil {
			t.Fatal(err)
		}
	}
	grant(column, UpdatePrivilege)
	roleExecute(t, e, op, "SET SESSION AUTHORIZATION update_reader")
	// Carrying protected b/c forward does not require SELECT on them.
	roleExecute(t, e, op, "UPDATE t SET a=7")
	for _, q := range []string{"UPDATE t SET b=8", "UPDATE t SET a=b", "UPDATE t SET a=8 WHERE c=3", "UPDATE t SET a=a"} {
		roleError(t, e, op, q, "42501", "permission denied for table t", "")
	}
	grant(column, UpdatePrivilege|SelectPrivilege)
	column.Address.SubID = 2
	grant(column, SelectPrivilege)
	roleExecute(t, e, op, "UPDATE t SET a=b WHERE a=7")
	roleExecute(t, e, op, "RESET SESSION AUTHORIZATION")
	result := roleExecute(t, e, op, "SELECT a,b,c FROM t")
	if got := tuples(result); !reflect.DeepEqual(got, [][]driver.Value{{int64(2), int64(2), int64(3)}}) {
		t.Fatal(got)
	}
}
