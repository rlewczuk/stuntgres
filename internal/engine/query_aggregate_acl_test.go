package engine

import (
	"context"
	"errors"
	"stuntgres/internal/engine/authorization"
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/sqlstate"
	"stuntgres/internal/sqltype"
	"testing"
)

func TestCountExpressionColumnACL(t *testing.T) {
	for _, prepared := range []bool{false, true} {
		for _, q := range []string{"SELECT count(*) FROM t", "SELECT count(v) FROM t", "SELECT count(k) FROM t", "SELECT count(k) FROM t WHERE FALSE", "SELECT count(*) FROM t GROUP BY k", "SELECT count(*) FROM t GROUP BY k HAVING k IS NULL", "SELECT count(*) FROM t GROUP BY k ORDER BY k", "SELECT count(v) FROM t HAVING count(k)>0", "SELECT count(v) FROM t ORDER BY count(k)"} {
			t.Run(q+map[bool]string{false: "/direct", true: "/prepared"}[prepared], func(t *testing.T) {
				e := New()
				op, done := roleOperation(t, e, "postgres")
				defer done()
				roleExecute(t, e, op, "CREATE USER aggregate_reader")
				roleExecute(t, e, op, "CREATE TABLE t(k TEXT,v INT)")
				worker, _ := e.Roles().Lookup("aggregate_reader")
				grantor := e.RoleStore.Published.Root.Read(authorization.PrincipalRef{}).LookupRef("postgres")
				table := roleTableRef(t, e, "t")
				grant := func(ref ids.ObjectRef, rights authorization.Privileges) {
					t.Helper()
					if err := e.SetObjectACL(context.Background(), ref, authorization.ACLValue{Explicit: true, Entries: []authorization.ACLEntry{{Grantor: grantor, Grantee: authorization.ACLGrantee{Role: worker.Ref}, Privileges: rights}}}); err != nil {
						t.Fatal(err)
					}
				}
				grant(table, 0)
				column := table
				column.Address.SubID = 2
				grant(column, authorization.SelectPrivilege)
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
				allowed := q == "SELECT count(*) FROM t" || q == "SELECT count(v) FROM t"
				execute(!allowed)
				if allowed {
					grant(column, 0)
					execute(true)
				}
			})
		}
	}
}

func TestAggregateModifiersColumnACL(t *testing.T) {
	for _, populated := range []bool{false, true} {
		e := New()
		op, done := roleOperation(t, e, "postgres")
		defer done()
		roleExecute(t, e, op, "CREATE USER am_unit_reader")
		roleExecute(t, e, op, "CREATE TABLE am_acl(v INT,hidden INT)")
		if populated {
			roleExecute(t, e, op, "INSERT INTO am_acl VALUES(1,1)")
		}
		reader, _ := e.Roles().Lookup("am_unit_reader")
		grantor := e.RoleStore.Published.Root.Read(authorization.PrincipalRef{}).LookupRef("postgres")
		grant := func(ref ids.ObjectRef, rights authorization.Privileges) {
			t.Helper()
			if err := e.SetObjectACL(context.Background(), ref, authorization.ACLValue{Explicit: true, Entries: []authorization.ACLEntry{{Grantor: grantor, Grantee: authorization.ACLGrantee{Role: reader.Ref}, Privileges: rights}}}); err != nil {
				t.Fatal(err)
			}
		}
		table := roleTableRef(t, e, "am_acl")
		grant(table, 0)
		visible, hidden := table, table
		visible.Address.SubID = 1
		hidden.Address.SubID = 2
		grant(visible, authorization.SelectPrivilege)
		roleExecute(t, e, op, "SET SESSION AUTHORIZATION am_unit_reader")
		for _, q := range []string{"SELECT count(v) FILTER(WHERE hidden>0) FROM am_acl WHERE FALSE", "SELECT count(v ORDER BY hidden) FILTER(WHERE FALSE) FROM am_acl", "SELECT GROUPING(hidden) FROM am_acl WHERE FALSE GROUP BY ROLLUP(hidden)", "SELECT GROUPING(hidden) FROM am_acl GROUP BY GROUPING SETS((hidden),()) HAVING FALSE"} {
			for _, prepared := range []bool{false, true} {
				ast := statement(t, q)
				var expected *StatementDescription
				if prepared {
					desc, err := e.Describe(op, ast)
					if err != nil {
						t.Fatal(err)
					}
					expected = &desc
				}
				scope, err := e.StartStatement(op)
				if err != nil {
					t.Fatal(err)
				}
				scope.Expected = expected
				result, err := scope.Execute(ast, 0, nil)
				err = scope.Finish(err)
				var got *sqlstate.Error
				want := sqlstate.Error{Severity: "ERROR", SeverityUnlocalized: "ERROR", Code: "42501", Message: "permission denied for table am_acl"}
				if !errors.As(err, &got) || *got != want || result.Rows != nil || result.Count != 0 {
					t.Fatal(q, prepared, result, got, err)
				}
			}
		}
		grant(hidden, authorization.SelectPrivilege)
		ast := statement(t, "SELECT count(v ORDER BY hidden) FILTER(WHERE hidden>0) FROM am_acl")
		held, err := e.Describe(op, ast)
		if err != nil {
			t.Fatal(err)
		}
		if len(held.Results) != 1 || held.Results[0].Name != "count" || held.Results[0].Usage.Type == nil || held.Results[0].Usage.Type.ID() != sqltype.Int8 {
			t.Fatal("modifier COUNT metadata", held.Results)
		}
		execute := func(denied bool, want int64) {
			t.Helper()
			scope, err := e.StartStatement(op)
			if err != nil {
				t.Fatal(err)
			}
			scope.Expected = &held
			result, err := scope.Execute(ast, 0, nil)
			err = scope.Finish(err)
			if denied {
				var got *sqlstate.Error
				want := sqlstate.Error{Severity: "ERROR", SeverityUnlocalized: "ERROR", Code: "42501", Message: "permission denied for table am_acl"}
				if !errors.As(err, &got) || *got != want || result.Rows != nil || result.Count != 0 {
					t.Fatal(result, got, err)
				}
				return
			}
			if err != nil || result.Rows == nil {
				t.Fatal(result, err)
			}
			got := tuples(result)
			result.Rows.Close()
			if len(got) != 1 || got[0][0] != want {
				t.Fatal(got, want)
			}
		}
		want := int64(0)
		if populated {
			want = 1
		}
		execute(false, want)
		grant(hidden, 0)
		execute(true, 0)
		grant(hidden, authorization.SelectPrivilege)
		ast = statement(t, "SELECT GROUPING(hidden) FROM am_acl GROUP BY ROLLUP(hidden) HAVING GROUPING(hidden)=1")
		held, err = e.Describe(op, ast)
		if err != nil {
			t.Fatal(err)
		}
		if len(held.Results) != 1 || held.Results[0].Name != "grouping" || held.Results[0].Usage.Type == nil || held.Results[0].Usage.Type.ID() != sqltype.Int4 {
			t.Fatal("held GROUPING metadata", held.Results)
		}
		execute(false, 1)
		grant(hidden, 0)
		execute(true, 0)
		grant(hidden, authorization.SelectPrivilege)
		execute(false, 1)
	}
}
