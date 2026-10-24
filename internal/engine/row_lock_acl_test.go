package engine

import (
	"context"
	"stuntgres/internal/engine/authorization"
	"stuntgres/internal/engine/ids"
	"testing"
)

func TestRowLockAuthorization(t *testing.T) {
	for _, clause := range rowLockTestClauses {
		for _, prepared := range []bool{false, true} {
			t.Run(clause+map[bool]string{false: "/direct", true: "/prepared"}[prepared], func(t *testing.T) {
				e := New()
				op, done := roleOperation(t, e, "postgres")
				defer done()
				for _, q := range []string{"CREATE USER row_lock_reader", "CREATE TABLE a(id INT,v INT)", "CREATE TABLE b(id INT,v INT)", "INSERT INTO a VALUES(1,10)", "INSERT INTO b VALUES(1,20)"} {
					roleExecute(t, e, op, q)
				}
				reader, _ := e.Roles().Lookup("row_lock_reader")
				grantor := e.roles.Published.Root.Read(authorization.PrincipalRef{}).LookupRef("postgres")
				grant := func(ref ids.ObjectRef, rights authorization.Privileges) {
					t.Helper()
					if err := e.SetObjectACL(context.Background(), ref, authorization.ACLValue{Explicit: true, Entries: []authorization.ACLEntry{{Grantor: grantor, Grantee: authorization.ACLGrantee{Role: reader.Ref}, Privileges: rights}}}); err != nil {
						t.Fatal(err)
					}
				}
				a, b := roleTableRef(t, e, "a"), roleTableRef(t, e, "b")
				grant(a, authorization.SelectPrivilege)
				grant(b, authorization.SelectPrivilege)
				roleExecute(t, e, op, "SET SESSION AUTHORIZATION row_lock_reader")
				query := "SELECT a.id FROM a JOIN b ON a.id=b.id " + clause
				ast := statement(t, query)
				if prepared {
					if _, err := e.Describe(op, ast); err != nil {
						t.Fatal(err)
					}
				}
				roleError(t, e, op, query, "42501", "permission denied for table a", "")
				// UPDATE on an unprojected column satisfies the extra locking requirement.
				av, bv := a, b
				av.Address.SubID = 2
				bv.Address.SubID = 2
				grant(av, authorization.UpdatePrivilege)
				roleError(t, e, op, query, "42501", "permission denied for table b", "")
				grant(bv, authorization.UpdatePrivilege)
				if prepared {
					if _, err := e.Describe(op, ast); err != nil {
						t.Fatal(err)
					}
				}
				result := roleExecute(t, e, op, query)
				if result.Rows == nil {
					t.Fatal("missing locker cursor")
				}
				result.Rows.Close()
				// UPDATE privilege must not replace SELECT authority on referenced columns.
				grant(b, 0)
				roleError(t, e, op, query, "42501", "permission denied for table b", "")
				grant(b, authorization.SelectPrivilege)
				grant(bv, 0)
				roleError(t, e, op, "SELECT a.id FROM a CROSS JOIN b WHERE FALSE "+clause, "42501", "permission denied for table b", "")
			})
		}
	}
}

func TestRowLockSequenceAuthorization(t *testing.T) {
	for _, clause := range rowLockTestClauses {
		t.Run(clause, func(t *testing.T) {
			e := New()
			op, done := roleOperation(t, e, "postgres")
			defer done()
			roleExecute(t, e, op, "CREATE USER row_lock_reader")
			roleExecute(t, e, op, "CREATE SEQUENCE s")
			reader, _ := e.Roles().Lookup("row_lock_reader")
			ref := roleTableRef(t, e, "s")
			grant := func(rights authorization.Privileges) {
				t.Helper()
				if err := e.SetObjectACL(context.Background(), ref, authorization.ACLValue{Explicit: true, Entries: []authorization.ACLEntry{{Grantor: e.roles.Published.Root.Read(authorization.PrincipalRef{}).LookupRef("postgres"), Grantee: authorization.ACLGrantee{Role: reader.Ref}, Privileges: rights}}}); err != nil {
					t.Fatal(err)
				}
			}
			grant(authorization.SelectPrivilege)
			roleExecute(t, e, op, "SET SESSION AUTHORIZATION row_lock_reader")
			ast := statement(t, "SELECT last_value FROM s "+clause)
			if _, err := e.Describe(op, ast); err != nil {
				t.Fatal("Prepare must not check executor ACL or row-mark kind", err)
			}
			roleError(t, e, op, "SELECT last_value FROM s "+clause, "42501", "permission denied for sequence s", "")
			grant(authorization.SelectPrivilege | authorization.UpdatePrivilege)
			roleError(t, e, op, "SELECT last_value FROM s "+clause, "42809", `cannot lock rows in sequence "s"`, "")
		})
	}
}
