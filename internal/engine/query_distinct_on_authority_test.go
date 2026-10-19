package engine

import (
	"context"
	"stuntgres/internal/engine/authorization"
	"stuntgres/internal/engine/ids"
	"testing"
)

func TestQueryDistinctOnPhysicalACLAndHeldRebind(t *testing.T) {
	for _, populated := range []bool{false, true} {
		t.Run(map[bool]string{false: "empty", true: "populated"}[populated], func(t *testing.T) {
			e := New()
			op, done := roleOperation(t, e, "postgres")
			defer done()
			roleExecute(t, e, op, "CREATE USER distinct_on_reader")
			roleExecute(t, e, op, "CREATE TABLE acl_t(id INT,secret INT)")
			if populated {
				roleExecute(t, e, op, "INSERT INTO acl_t VALUES(1,9),(1,8),(2,8)")
			}
			roleExecute(t, e, op, "CREATE VIEW invoker_on WITH(security_invoker=true) AS SELECT DISTINCT ON(secret) id FROM acl_t ORDER BY secret,id DESC")
			reader, _ := e.Roles().Lookup("distinct_on_reader")
			grantor := e.roles.published.Root.Read(authorization.PrincipalRef{}).LookupRef("postgres")
			table := roleTableRef(t, e, "acl_t")
			id := table
			id.Address.SubID = 1
			grant := func(ref ids.ObjectRef, rights authorization.Privileges) {
				t.Helper()
				if err := e.SetObjectACL(context.Background(), ref, authorization.ACLValue{Explicit: true, Entries: []authorization.ACLEntry{{Grantor: grantor, Grantee: authorization.ACLGrantee{Role: reader.Ref}, Privileges: rights}}}); err != nil {
					t.Fatal(err)
				}
			}
			grant(table, 0)
			grant(id, authorization.SelectPrivilege)
			grant(roleTableRef(t, e, "invoker_on"), authorization.SelectPrivilege)
			roleExecute(t, e, op, "SET SESSION AUTHORIZATION distinct_on_reader")
			ast := statement(t, "SELECT DISTINCT ON(secret) id AS secret FROM acl_t ORDER BY secret")
			held, err := e.Describe(op, ast)
			if err != nil || len(held.Results) != 1 || held.Results[0].OriginAttribute != 1 {
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
					assertDistinctACLFailure(t, result, err)
					return
				}
				want := 0
				if populated {
					want = 2
				}
				if err != nil || result.Rows == nil {
					t.Fatal(result, err)
				}
				if got := len(tuples(result)); got != want {
					result.Rows.Close()
					t.Fatal(got, want)
				}
				result.Rows.Close()
			}
			execute(false)
			for _, q := range []string{
				"SELECT DISTINCT ON(secret) id FROM acl_t",
				"SELECT DISTINCT ON(secret) id FROM acl_t WHERE FALSE",
				"SELECT DISTINCT ON(secret) id FROM acl_t LIMIT 0",
				"SELECT DISTINCT ON(acl_t.secret) id AS secret FROM acl_t",
				"SELECT DISTINCT ON((SELECT secret FROM acl_t LIMIT 1)) id FROM acl_t",
				"SELECT id FROM invoker_on",
			} {
				result, err := e.Execute(op, statement(t, q), 0, nil)
				assertDistinctACLFailure(t, result, err)
			}
			grant(id, 0)
			execute(true)
			grant(id, authorization.SelectPrivilege)
			execute(false)
		})
	}
}
