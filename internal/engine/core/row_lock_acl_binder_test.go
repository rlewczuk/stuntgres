package core_test

import (
	"context"
	"testing"

	"stuntgres/internal/engine"
	"stuntgres/internal/engine/authorization"
	"stuntgres/internal/engine/enginetest"
)

func TestRowLockSequenceAuthorization(t *testing.T) {
	for _, clause := range rowLockTestClauses {
		t.Run(clause, func(t *testing.T) {
			e := engine.New()
			op, done := enginetest.RoleOperation(t, e, "postgres")
			defer done()
			enginetest.RoleExecute(t, e, op, "CREATE USER row_lock_reader")
			enginetest.RoleExecute(t, e, op, "CREATE SEQUENCE s")
			reader, _ := e.Roles().Lookup("row_lock_reader")
			ref := enginetest.RoleTableRef(t, e, "s")
			grant := func(rights authorization.Privileges) {
				t.Helper()
				if err := enginetest.SetObjectACL(e, context.Background(), ref, authorization.ACLValue{Explicit: true, Entries: []authorization.ACLEntry{{Grantor: e.RoleStore.Published.Root.Read(authorization.PrincipalRef{}).LookupRef("postgres"), Grantee: authorization.ACLGrantee{Role: reader.Ref}, Privileges: rights}}}); err != nil {
					t.Fatal(err)
				}
			}
			grant(authorization.SelectPrivilege)
			enginetest.RoleExecute(t, e, op, "SET SESSION AUTHORIZATION row_lock_reader")
			ast := enginetest.Statement(t, "SELECT last_value FROM s "+clause)
			if _, err := e.Describe(op, ast); err != nil {
				t.Fatal("Prepare must not check executor ACL or row-mark kind", err)
			}
			enginetest.RoleError(t, e, op, "SELECT last_value FROM s "+clause, "42501", "permission denied for sequence s", "")
			grant(authorization.SelectPrivilege | authorization.UpdatePrivilege)
			enginetest.RoleError(t, e, op, "SELECT last_value FROM s "+clause, "42809", `cannot lock rows in sequence "s"`, "")
		})
	}
}
