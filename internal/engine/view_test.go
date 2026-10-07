package engine

import (
	"context"
	"errors"
	"stuntgres/internal/sqlstate"
	"testing"
)

func TestViewOriginalInvokerAuthority(t *testing.T) {
	e := New()
	admin, done := roleOperation(t, e, "postgres")
	defer done()
	for _, q := range []string{"CREATE USER view_reader", "CREATE TABLE t(id INT)", "INSERT INTO t VALUES(7)", "CREATE VIEW inner_view WITH(security_invoker=true) AS SELECT id FROM t", "CREATE VIEW outer_view AS SELECT id FROM inner_view"} {
		roleExecute(t, e, admin, q)
	}
	reader, _ := e.Roles().Lookup("view_reader")
	grantor := e.roles.names["postgres"]
	grant := func(name string, rights Privileges) {
		t.Helper()
		ref := roleTableRef(t, e, name)
		if err := e.SetObjectACL(context.Background(), ref, ACLValue{Explicit: true, Entries: []ACLEntry{{Grantor: grantor, Grantee: ACLGrantee{Role: reader.Ref}, Privileges: rights}}}); err != nil {
			t.Fatal(err)
		}
	}
	grant("outer_view", SelectPrivilege)
	grant("t", 0)
	roleExecute(t, e, admin, "SET SESSION AUTHORIZATION view_reader")
	ast := statement(t, "SELECT id FROM outer_view")
	execute := func(denied bool) {
		t.Helper()
		scope, err := e.StartStatement(admin)
		if err != nil {
			t.Fatal(err)
		}
		result, err := scope.Execute(ast, 0, nil)
		err = scope.Finish(err)
		if denied {
			var diagnostic *sqlstate.Error
			if !errors.As(err, &diagnostic) || diagnostic.Code != "42501" || diagnostic.Message != "permission denied for table t" || result.Rows != nil {
				t.Fatal(result, err)
			}
		} else {
			if err != nil || result.Count != 1 || result.Rows == nil {
				t.Fatal(result, err)
			}
			result.Rows.Close()
		}
	}
	execute(true)
	grant("t", SelectPrivilege)
	execute(false)
	grant("t", 0)
	execute(true)
}

func TestViewCreateDoesNotExecuteDefinition(t *testing.T) {
	e := New()
	run(t, e, "CREATE SEQUENCE s")
	run(t, e, "CREATE TABLE t(id INT)")
	run(t, e, "INSERT INTO t VALUES(1)")
	run(t, e, "CREATE VIEW v AS SELECT nextval('s') FROM t")
	result := run(t, e, "SELECT last_value,is_called FROM s")
	rows := tuples(result)
	if len(rows) != 1 || rows[0][0] != int64(1) || rows[0][1] != false {
		t.Fatal(rows)
	}
}
