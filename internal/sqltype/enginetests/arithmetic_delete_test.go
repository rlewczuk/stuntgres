package enginetests_test

import (
	"context"
	"database/sql/driver"
	"errors"
	"reflect"
	"testing"

	"stuntgres/internal/engine"
	"stuntgres/internal/engine/authorization"
	"stuntgres/internal/engine/core"
	"stuntgres/internal/engine/enginetest"
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/sqlstate"
	"stuntgres/internal/sqltype"
)

func TestExpressionArithmeticDeleteAuthorization(t *testing.T) {
	for _, query := range []string{"DELETE FROM t WHERE i+1=8", "DELETE FROM t WHERE CAST(i AS bigint)+1=8", "DELETE FROM t WHERE b+i=14"} {
		for _, prepared := range []bool{false, true} {
			t.Run(query+map[bool]string{false: "/direct", true: "/prepared"}[prepared], func(t *testing.T) {
				e := engine.New()
				op, done := enginetest.RoleOperation(t, e, "postgres")
				defer done()
				enginetest.RoleExecute(t, e, op, "CREATE USER reader")
				enginetest.RoleExecute(t, e, op, "CREATE TABLE t(id INT,i INT,b BIGINT)")
				enginetest.RoleExecute(t, e, op, "INSERT INTO t VALUES(1,7,7),(2,-7,-7),(3,NULL,NULL),(4,7,7)")
				ref := enginetest.RoleTableRef(t, e, "t")
				owner := op.RoleState().CurrentUser
				reader, _ := e.Roles().Lookup("reader")
				grant := func(object ids.ObjectRef, mask authorization.Privileges) {
					t.Helper()
					if err := enginetest.SetObjectACL(e, context.Background(), object, authorization.ACLValue{Explicit: true, Entries: []authorization.ACLEntry{{Grantor: owner, Grantee: authorization.ACLGrantee{Role: reader.Ref}, Privileges: mask}}}); err != nil {
						t.Fatal(err)
					}
				}
				grant(ref, authorization.DeletePrivilege)
				enginetest.RoleExecute(t, e, op, "SET SESSION AUTHORIZATION reader")
				ast := enginetest.Statement(t, query)
				description, err := e.Describe(op, ast)
				if err != nil {
					t.Fatal("ACL moved to Prepare", err)
				}
				execute := func() (core.Result, error) {
					if !prepared {
						return e.Execute(op, ast, 0, nil)
					}
					scope, err := e.StartStatement(op)
					if err != nil {
						return core.Result{}, err
					}
					scope.Expected = &description
					result, err := scope.Execute(ast, 0, nil)
					return result, scope.Finish(err)
				}
				denied := func() {
					t.Helper()
					before := e.Catalog.State
					result, err := execute()
					var diagnostic *sqlstate.Error
					want := sqlstate.Error{Severity: "ERROR", SeverityUnlocalized: "ERROR", Code: "42501", Message: "permission denied for table t"}
					if result.Count != 0 || result.Rows != nil || !errors.As(err, &diagnostic) || *diagnostic != want || e.Catalog.State != before {
						t.Fatal(result, err, diagnostic)
					}
				}
				denied()
				column := ref
				column.Address.SubID = 2
				grant(column, authorization.SelectPrivilege)
				if query == "DELETE FROM t WHERE b+i=14" {
					denied()
					column.Address.SubID = 3
					grant(column, authorization.SelectPrivilege)
				}
				result, err := execute()
				if err != nil || result.Count != 2 || result.Rows != nil {
					t.Fatal(result, err)
				}
				parameter, err := e.Describe(op, enginetest.Statement(t, "DELETE FROM t WHERE i+$1=9"))
				if err != nil || parameter.Parameters.Count != 1 || len(parameter.Parameters.Types) != 1 || parameter.Parameters.Types[0].Type.ID() != sqltype.Int4 {
					t.Fatal(parameter, err)
				}
				enginetest.RoleExecute(t, e, op, "RESET SESSION AUTHORIZATION")
				remaining := enginetest.RoleExecute(t, e, op, "SELECT id,i,b FROM t ORDER BY id")
				defer remaining.Rows.Close()
				if !reflect.DeepEqual(enginetest.Tuples(remaining), [][]driver.Value{{int64(2), int64(-7), int64(-7)}, {int64(3), nil, nil}}) {
					t.Fatal("authorization changed survivor multiplicity")
				}
			})
		}
	}
}
