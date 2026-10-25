package core_test

import (
	"context"
	"testing"

	"stuntgres/internal/engine"
	"stuntgres/internal/engine/authorization"
	"stuntgres/internal/engine/enginetest"
	"stuntgres/internal/engine/ids"
)

func TestRowLockAuthorization(t *testing.T) {
	for _, clause := range rowLockTestClauses {
		for _, prepared := range []bool{false, true} {
			t.Run(clause+map[bool]string{false: "/direct", true: "/prepared"}[prepared], func(t *testing.T) {
				e := engine.New()
				op, done := enginetest.RoleOperation(t, e, "postgres")
				defer done()
				for _, q := range []string{"CREATE USER row_lock_reader", "CREATE TABLE a(id INT,v INT)", "CREATE TABLE b(id INT,v INT)", "INSERT INTO a VALUES(1,10)", "INSERT INTO b VALUES(1,20)"} {
					enginetest.RoleExecute(t, e, op, q)
				}
				reader, _ := e.Roles().Lookup("row_lock_reader")
				grantor := e.RoleStore.Published.Root.Read(authorization.PrincipalRef{}).LookupRef("postgres")
				grant := func(ref ids.ObjectRef, rights authorization.Privileges) {
					t.Helper()
					if err := enginetest.SetObjectACL(e, context.Background(), ref, authorization.ACLValue{Explicit: true, Entries: []authorization.ACLEntry{{Grantor: grantor, Grantee: authorization.ACLGrantee{Role: reader.Ref}, Privileges: rights}}}); err != nil {
						t.Fatal(err)
					}
				}
				a, b := enginetest.RoleTableRef(t, e, "a"), enginetest.RoleTableRef(t, e, "b")
				grant(a, authorization.SelectPrivilege)
				grant(b, authorization.SelectPrivilege)
				enginetest.RoleExecute(t, e, op, "SET SESSION AUTHORIZATION row_lock_reader")
				query := "SELECT a.id FROM a JOIN b ON a.id=b.id " + clause
				ast := enginetest.Statement(t, query)
				if prepared {
					if _, err := e.Describe(op, ast); err != nil {
						t.Fatal(err)
					}
				}
				enginetest.RoleError(t, e, op, query, "42501", "permission denied for table a", "")
				// UPDATE on an unprojected column satisfies the extra locking requirement.
				av, bv := a, b
				av.Address.SubID = 2
				bv.Address.SubID = 2
				grant(av, authorization.UpdatePrivilege)
				enginetest.RoleError(t, e, op, query, "42501", "permission denied for table b", "")
				grant(bv, authorization.UpdatePrivilege)
				if prepared {
					if _, err := e.Describe(op, ast); err != nil {
						t.Fatal(err)
					}
				}
				result := enginetest.RoleExecute(t, e, op, query)
				if result.Rows == nil {
					t.Fatal("missing locker cursor")
				}
				result.Rows.Close()
				// UPDATE privilege must not replace SELECT authority on referenced columns.
				grant(b, 0)
				enginetest.RoleError(t, e, op, query, "42501", "permission denied for table b", "")
				grant(b, authorization.SelectPrivilege)
				grant(bv, 0)
				enginetest.RoleError(t, e, op, "SELECT a.id FROM a CROSS JOIN b WHERE FALSE "+clause, "42501", "permission denied for table b", "")
			})
		}
	}
}

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
