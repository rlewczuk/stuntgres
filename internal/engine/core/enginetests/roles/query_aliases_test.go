package roles_test

import (
	"context"
	"database/sql/driver"
	"errors"
	"reflect"
	"testing"

	"stuntgres/internal/engine"
	"stuntgres/internal/engine/authorization"
	"stuntgres/internal/engine/enginetest"
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/sqlstate"
)

func TestQueryAliasesPhysicalACL(t *testing.T) {
	for _, populated := range []bool{false, true} {
		e := engine.New()
		op, done := enginetest.RoleOperation(t, e, "postgres")
		defer done()
		enginetest.RoleExecute(t, e, op, "CREATE USER alias_reader")
		enginetest.RoleExecute(t, e, op, "CREATE TABLE acl_t(id INT,secret INT)")
		if populated {
			enginetest.RoleExecute(t, e, op, "INSERT INTO acl_t VALUES(1,9),(2,8)")
		}
		reader, _ := e.Roles().Lookup("alias_reader")
		grantor := e.RoleStore.Published.Root.Read(authorization.PrincipalRef{}).LookupRef("postgres")
		grant := func(ref ids.ObjectRef, rights authorization.Privileges) {
			t.Helper()
			if err := enginetest.SetObjectACL(e, context.Background(), ref, authorization.ACLValue{Explicit: true, Entries: []authorization.ACLEntry{{Grantor: grantor, Grantee: authorization.ACLGrantee{Role: reader.Ref}, Privileges: rights}}}); err != nil {
				t.Fatal(err)
			}
		}
		table := enginetest.RoleTableRef(t, e, "acl_t")
		grant(table, 0)
		id := table
		id.Address.SubID = 1
		grant(id, authorization.SelectPrivilege)
		enginetest.RoleExecute(t, e, op, "SET SESSION AUTHORIZATION alias_reader")
		ast := enginetest.Statement(t, "SELECT x.iid AS secret FROM acl_t x(iid) ORDER BY secret")
		held, err := e.Describe(op, ast)
		if err != nil || len(held.Results) != 1 || held.Results[0].OriginAttribute != 1 || held.Results[0].Name != "secret" {
			t.Fatal(held, err)
		}
		execute := func(denied bool) {
			t.Helper()
			scope, err := e.StartStatement(op)
			if err != nil {
				t.Fatal(err)
			}
			scope.Expected = &held
			result, err := scope.Execute(ast, 0, nil)
			err = scope.Finish(err)
			if denied {
				want := sqlstate.Error{Severity: "ERROR", SeverityUnlocalized: "ERROR", Code: "42501", Message: "permission denied for table acl_t"}
				var got *sqlstate.Error
				if !errors.As(err, &got) || *got != want || result.Rows != nil || result.Count != 0 {
					t.Fatal(result, got, err, want)
				}
				return
			}
			if err != nil || result.Rows == nil {
				t.Fatal(result, err)
			}
			want := [][]driver.Value{}
			if populated {
				want = [][]driver.Value{{int64(1)}, {int64(2)}}
			}
			got := enginetest.Tuples(result)
			result.Rows.Close()
			if !reflect.DeepEqual(got, want) {
				t.Fatal(got, want)
			}
		}
		execute(false)
		for _, q := range []string{"SELECT x.secret FROM acl_t x(iid)", "SELECT x.iid AS secret FROM acl_t x(iid) WHERE secret=9"} {
			result, err := e.Execute(op, enginetest.Statement(t, q), 0, nil)
			var got *sqlstate.Error
			want := sqlstate.Error{Severity: "ERROR", SeverityUnlocalized: "ERROR", Code: "42501", Message: "permission denied for table acl_t"}
			if !errors.As(err, &got) || *got != want || result.Rows != nil || result.Count != 0 {
				t.Fatal(q, result, got, err)
			}
		}
		grant(id, 0)
		execute(true)
		// An empty source and constant output must still require relation SELECT.
		result, err := e.Execute(op, enginetest.Statement(t, "SELECT 1 AS secret FROM acl_t x(iid) WHERE FALSE"), 0, nil)
		var got *sqlstate.Error
		want := sqlstate.Error{Severity: "ERROR", SeverityUnlocalized: "ERROR", Code: "42501", Message: "permission denied for table acl_t"}
		if !errors.As(err, &got) || *got != want || result.Rows != nil || result.Count != 0 {
			t.Fatal(result, got, err)
		}
	}
}
