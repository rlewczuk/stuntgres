package engine

import (
	"context"
	"stuntgres/internal/engine/authorization"
	"testing"
)

func TestTimezoneReviewQualifiedLiteralAuthorization(t *testing.T) {
	e := New()
	op, done := roleOperation(t, e, "postgres")
	defer done()
	roleExecute(t, e, op, "CREATE USER tz_literal_reader")
	roleExecute(t, e, op, "CREATE TABLE tz_literal_anchor(id INT)")
	roleExecute(t, e, op, "INSERT INTO tz_literal_anchor VALUES(1)")
	reader, _ := e.Roles().Lookup("tz_literal_reader")
	if err := e.SetObjectACL(context.Background(), roleTableRef(t, e, "public.tz_literal_anchor"), authorization.ACLValue{Explicit: true, Entries: []authorization.ACLEntry{{Grantor: e.roles.published.Root.Read(authorization.PrincipalRef{}).LookupRef("postgres"), Grantee: authorization.ACLGrantee{Role: reader.Ref}, Privileges: authorization.SelectPrivilege}}}); err != nil {
		t.Fatal(err)
	}
	if err := e.SetObjectACL(context.Background(), schemaRef(t, e, "pg_catalog"), authorization.ACLValue{Explicit: true}); err != nil {
		t.Fatal(err)
	}
	roleExecute(t, e, op, "SET SESSION AUTHORIZATION tz_literal_reader")
	for _, name := range []string{"timestamptz", "\"timestamptz\"", "timetz", "\"timetz\""} {
		input := "2004-07-01 12:00+00"
		if name == "timetz" || name == "\"timetz\"" {
			input = "05:34:17-05"
		}
		q := "SELECT pg_catalog." + name + " '" + input + "' FROM tz_literal_anchor"
		roleErrorPosition(t, e, op, q, "42501", "permission denied for schema pg_catalog", "", "8")
		_, err := e.Describe(op, statement(t, q))
		state(t, err, "42501")
	}
}
