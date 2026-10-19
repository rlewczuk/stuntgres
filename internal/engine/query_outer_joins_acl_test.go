package engine

import (
	"context"
	"stuntgres/internal/engine/authorization"
	"stuntgres/internal/engine/ids"
	"testing"
)

func TestQueryOuterJoinsBothUsingKeysACL(t *testing.T) {
	for _, kind := range []string{"LEFT", "RIGHT", "FULL"} {
		for _, denied := range []string{"a", "b"} {
			e := New()
			op, done := roleOperation(t, e, "postgres")
			defer done()
			for _, sql := range []string{"CREATE USER outer_key_reader", "CREATE TABLE a(k INT)", "CREATE TABLE b(k BIGINT)"} {
				roleExecute(t, e, op, sql)
			}
			reader, _ := e.Roles().Lookup("outer_key_reader")
			grantor := e.roles.published.Root.Read(authorization.PrincipalRef{}).LookupRef("postgres")
			grant := func(ref ids.ObjectRef, rights authorization.Privileges) {
				t.Helper()
				if err := e.SetObjectACL(context.Background(), ref, authorization.ACLValue{Explicit: true, Entries: []authorization.ACLEntry{{Grantor: grantor, Grantee: authorization.ACLGrantee{Role: reader.Ref}, Privileges: rights}}}); err != nil {
					t.Fatal(err)
				}
			}
			for _, name := range []string{"a", "b"} {
				ref := roleTableRef(t, e, name)
				grant(ref, 0)
				ref.Address.SubID = 1
				grant(ref, authorization.SelectPrivilege)
			}
			roleExecute(t, e, op, "SET SESSION AUTHORIZATION outer_key_reader")
			ast := statement(t, "SELECT 1 FROM a "+kind+" JOIN b USING(k) LIMIT 0")
			description, err := e.Describe(op, ast)
			if err != nil {
				t.Fatal(err)
			}
			scope, err := e.StartStatement(op)
			if err != nil {
				t.Fatal(err)
			}
			scope.Expected = &description
			result, err := scope.Execute(ast, 0, nil)
			err = scope.Finish(err)
			if err != nil || result.Rows == nil || result.Count != 0 {
				t.Fatal(result, err)
			}
			result.Rows.Close()
			ref := roleTableRef(t, e, denied)
			ref.Address.SubID = 1
			grant(ref, 0)
			scope, err = e.StartStatement(op)
			if err != nil {
				t.Fatal(err)
			}
			scope.Expected = &description
			result, err = scope.Execute(ast, 0, nil)
			err = scope.Finish(err)
			state(t, err, "42501")
			if result.Rows != nil {
				result.Rows.Close()
				t.Fatal("denied empty join exposed rows")
			}
		}
	}
}
