package engine

import (
	"context"
	"stuntgres/internal/sqlstate"
	"stuntgres/internal/sqltype"
	"testing"
)

func TestCheckInsertPrivilegeDisclosure(t *testing.T) {
	for _, mode := range []string{"none", "readable", "all_columns", "table"} {
		for _, prepared := range []bool{false, true} {
			t.Run(mode+map[bool]string{false: "direct", true: "prepared"}[prepared], func(t *testing.T) {
				e := New()
				admin, done := roleOperation(t, e, "postgres")
				defer done()
				roleExecute(t, e, admin, "CREATE ROLE check_actor")
				roleExecute(t, e, admin, "CREATE TABLE t(n INT,secret TEXT DEFAULT 'private',readable TEXT DEFAULT 'visible',CONSTRAINT c CHECK(n=1 AND secret='private'))")
				ref := roleTableRef(t, e, "t")
				actor, _ := e.Roles().Lookup("check_actor")
				grant := func(attribute int32, privileges Privileges) {
					target := ref
					target.Address.SubID = attribute
					if err := e.SetObjectACL(context.Background(), target, ACLValue{Explicit: true, Entries: []ACLEntry{{Grantor: e.roles.names["postgres"], Grantee: ACLGrantee{Role: actor.Ref}, Privileges: privileges}}}); err != nil {
						t.Fatal(err)
					}
				}
				grant(1, InsertPrivilege)
				detail := "Failing row contains (n) = (2)."
				switch mode {
				case "readable":
					grant(3, SelectPrivilege)
					detail = "Failing row contains (n, readable) = (2, visible)."
				case "all_columns":
					grant(1, InsertPrivilege|SelectPrivilege)
					grant(2, SelectPrivilege)
					grant(3, SelectPrivilege)
					detail = "Failing row contains (n, secret, readable) = (2, private, visible)."
				case "table":
					grant(0, SelectPrivilege)
					detail = "Failing row contains (2, private, visible)."
				}
				op, finish := roleOperation(t, e, "check_actor")
				defer finish()
				query := "INSERT INTO t(n) VALUES(2)"
				count := 0
				if prepared {
					query = "INSERT INTO t(n) VALUES($1)"
					count = 1
				}
				ast := statement(t, query)
				scope, err := e.StartStatement(op)
				if err != nil {
					t.Fatal(err)
				}
				if prepared {
					description, err := e.Describe(op, ast)
					if err != nil {
						scope.Finish(err)
						t.Fatal(err)
					}
					scope.Expected = &description
				}
				result, err := scope.Execute(ast, count, func(ctx context.Context, parameters ParameterInfo, frame []sqltype.Value, input sqltype.InputEnvironment) error {
					value, err := sqltype.Integer(parameters.Types[0].Type, 2)
					frame[0] = value
					return err
				})
				scope.Finish(err)
				if result.Count != 0 || result.Rows != nil {
					t.Fatal(result)
				}
				nullCountUnitDiagnostic(t, err, sqlstate.Error{Code: "23514", Message: `new row for relation "t" violates check constraint "c"`, Detail: detail, Schema: "public", Table: "t", Constraint: "c"})
				result = roleExecute(t, e, admin, "SELECT * FROM t")
				if len(tuples(result)) != 0 {
					t.Fatal("failed INSERT published rows")
				}
				result.Rows.Close()
				lockEmpty(t, e)
			})
		}
	}
}
