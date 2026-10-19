package engine

import (
	"context"
	"errors"
	"stuntgres/internal/engine/authorization"
	"stuntgres/internal/sqlstate"
	"testing"
)

func TestViewJoinedOriginalInvokerAuthority(t *testing.T) {
	e := New()
	op, done := roleOperation(t, e, "postgres")
	defer done()
	for _, q := range []string{"CREATE USER joined_reader", "CREATE TABLE a(id INT)", "CREATE TABLE b(hidden INT)", "INSERT INTO a VALUES(7)", "INSERT INTO b VALUES(7)", "CREATE VIEW inner_view WITH(security_invoker=true,security_barrier=true) AS SELECT a.id FROM a JOIN b ON a.id=b.hidden", "CREATE VIEW outer_view AS SELECT id FROM inner_view"} {
		roleExecute(t, e, op, q)
	}
	reader, _ := e.Roles().Lookup("joined_reader")
	grantor := e.roles.published.root.Read(authorization.PrincipalRef{}).LookupRef("postgres")
	grant := func(name string, rights authorization.Privileges) {
		t.Helper()
		if err := e.SetObjectACL(context.Background(), roleTableRef(t, e, name), authorization.ACLValue{Explicit: true, Entries: []authorization.ACLEntry{{Grantor: grantor, Grantee: authorization.ACLGrantee{Role: reader.Ref}, Privileges: rights}}}); err != nil {
			t.Fatal(err)
		}
	}
	for _, name := range []string{"a", "b", "inner_view", "outer_view"} {
		grant(name, authorization.SelectPrivilege)
	}
	roleExecute(t, e, op, "SET SESSION AUTHORIZATION joined_reader")
	for _, name := range []string{"inner_view", "outer_view"} {
		ast := statement(t, "SELECT id FROM "+name)
		description, err := e.Describe(op, ast)
		if err != nil {
			t.Fatal(err)
		}
		if len(description.Results) != 1 || description.Results[0].Name != "id" || description.Results[0].Usage.Type.DatabaseName() != "INT4" {
			t.Fatal(description.Results)
		}
		saved := description.Results[0]
		execute := func(prepared, denied, empty bool) {
			t.Helper()
			before := e.catalog.state
			scope, err := e.StartStatement(op)
			if err != nil {
				t.Fatal(err)
			}
			if prepared {
				scope.Expected = &description
			}
			result, err := scope.Execute(ast, 0, nil)
			err = scope.Finish(err)
			if denied {
				var diagnostic *sqlstate.Error
				want := sqlstate.Error{Severity: "ERROR", SeverityUnlocalized: "ERROR", Code: "42501", Message: "permission denied for table b"}
				if !errors.As(err, &diagnostic) || *diagnostic != want || result.Rows != nil || result.Count != 0 || e.catalog.state != before {
					t.Fatal(name, prepared, result, err)
				}
				return
			}
			if err != nil || result.Rows == nil {
				t.Fatal(result, err)
			}
			defer result.Rows.Close()
			schema := result.Rows.Schema()
			if len(schema) != 1 || schema[0] != saved || description.Results[0] != saved {
				t.Fatal(schema, description.Results)
			}
			rows := tuples(result)
			if empty {
				if len(rows) != 0 {
					t.Fatal(rows)
				}
			} else if len(rows) != 1 || len(rows[0]) != 1 || rows[0][0] != int64(7) {
				t.Fatal(rows)
			}
		}
		execute(true, false, false)
		grant("b", 0)
		execute(false, true, false)
		execute(true, true, false)
		roleExecute(t, e, op, "SET SESSION AUTHORIZATION postgres")
		roleExecute(t, e, op, "DELETE FROM b")
		roleExecute(t, e, op, "SET SESSION AUTHORIZATION joined_reader")
		execute(false, true, true)
		execute(true, true, true)
		grant("b", authorization.SelectPrivilege)
		execute(false, false, true)
		execute(true, false, true)
		roleExecute(t, e, op, "SET SESSION AUTHORIZATION postgres")
		roleExecute(t, e, op, "INSERT INTO b VALUES(7)")
		roleExecute(t, e, op, "SET SESSION AUTHORIZATION joined_reader")
		execute(false, false, false)
		execute(true, false, false)
	}
}
