package engine

import (
	"context"
	"errors"
	"stuntgres/internal/sqlstate"
	"testing"
)

func TestQueryAggregateColumnACL(t *testing.T) {
	for _, prepared := range []bool{false, true} {
		for _, q := range []string{"SELECT count(*) FROM t", "SELECT count(*) FROM t GROUP BY k", "SELECT count(*) FROM t GROUP BY k HAVING k IS NULL", "SELECT count(*) FROM t GROUP BY k ORDER BY k"} {
			t.Run(q+map[bool]string{false: "/direct", true: "/prepared"}[prepared], func(t *testing.T) {
				e := New()
				op, done := roleOperation(t, e, "postgres")
				defer done()
				roleExecute(t, e, op, "CREATE USER aggregate_reader")
				roleExecute(t, e, op, "CREATE TABLE t(k TEXT,v INT)")
				worker, _ := e.Roles().Lookup("aggregate_reader")
				grantor := e.roles.names["postgres"]
				table := roleTableRef(t, e, "t")
				grant := func(ref ObjectRef, rights Privileges) {
					t.Helper()
					if err := e.SetObjectACL(context.Background(), ref, ACLValue{Explicit: true, Entries: []ACLEntry{{Grantor: grantor, Grantee: ACLGrantee{Role: worker.Ref}, Privileges: rights}}}); err != nil {
						t.Fatal(err)
					}
				}
				grant(table, 0)
				column := table
				column.Address.SubID = 2
				grant(column, SelectPrivilege)
				roleExecute(t, e, op, "SET SESSION AUTHORIZATION aggregate_reader")
				ast := statement(t, q)
				var description *StatementDescription
				if prepared {
					desc, err := e.Describe(op, ast)
					if err != nil {
						t.Fatal(err)
					}
					description = &desc
				}
				execute := func(wantDenied bool) {
					t.Helper()
					scope, err := e.StartStatement(op)
					if err != nil {
						t.Fatal(err)
					}
					scope.Expected = description
					result, err := scope.Execute(ast, 0, nil)
					err = scope.Finish(err)
					if wantDenied {
						var got *sqlstate.Error
						want := sqlstate.Error{Severity: "ERROR", SeverityUnlocalized: "ERROR", Code: "42501", Message: "permission denied for table t"}
						if !errors.As(err, &got) || *got != want || result.Rows != nil || result.Count != 0 {
							t.Fatal(result, got, err)
						}
						return
					}
					if err != nil || result.Rows == nil || result.Count != 1 {
						t.Fatal(result, err)
					}
					result.Rows.Close()
				}
				execute(q != "SELECT count(*) FROM t")
				if q == "SELECT count(*) FROM t" {
					grant(column, 0)
					execute(true)
				}
			})
		}
	}
}
