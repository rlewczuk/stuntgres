package engine

import (
	"context"
	"testing"
)

func TestRowLockViewSequenceDiagnostics(t *testing.T) {
	e := New()
	op, done := roleOperation(t, e, "postgres")
	defer done()
	for _, q := range []string{"CREATE USER sequence_reader", "CREATE SEQUENCE s", "CREATE VIEW v WITH(security_invoker=true) AS SELECT last_value FROM s"} {
		roleExecute(t, e, op, q)
	}
	for _, clause := range rowLockTestClauses {
		q := "SELECT last_value FROM v WHERE FALSE " + clause
		if _, err := e.Describe(op, statement(t, q)); err != nil {
			t.Fatal("kind check must be execution-time", err)
		}
		roleError(t, e, op, q, "42809", `cannot lock rows in sequence "s"`, "")
		lockEmpty(t, e)
	}
	reader, _ := e.Roles().Lookup("sequence_reader")
	grantor := e.roles.names["postgres"]
	entry := ACLEntry{Grantor: grantor, Grantee: ACLGrantee{Role: reader.Ref}, Privileges: SelectPrivilege}
	for _, name := range []string{"s", "v"} {
		rights := entry
		if name == "v" {
			rights.Privileges |= UpdatePrivilege
		}
		if err := e.SetObjectACL(context.Background(), roleTableRef(t, e, name), ACLValue{Explicit: true, Entries: []ACLEntry{rights}}); err != nil {
			t.Fatal(err)
		}
	}
	roleExecute(t, e, op, "SET SESSION AUTHORIZATION sequence_reader")
	for _, clause := range rowLockTestClauses {
		q := "SELECT last_value FROM v " + clause
		if _, err := e.Describe(op, statement(t, q)); err != nil {
			t.Fatal(err)
		}
		roleError(t, e, op, q, "42501", "permission denied for sequence s", "")
	}
	roleExecute(t, e, op, "RESET SESSION AUTHORIZATION")
	lockEmpty(t, e)
}
