package enginetests_test

import (
	"context"
	"testing"

	"stuntgres/internal/engine"
	"stuntgres/internal/engine/authorization"
	"stuntgres/internal/engine/core"
	"stuntgres/internal/engine/enginetest"
	"stuntgres/internal/engine/ids"
)

func TestTimezoneReviewQualifiedLiteralAuthorization(t *testing.T) {
	e := engine.New()
	op, done := enginetest.RoleOperation(t, e, "postgres")
	defer done()
	enginetest.RoleExecute(t, e, op, "CREATE USER tz_literal_reader")
	enginetest.RoleExecute(t, e, op, "CREATE TABLE tz_literal_anchor(id INT)")
	enginetest.RoleExecute(t, e, op, "INSERT INTO tz_literal_anchor VALUES(1)")
	reader, _ := e.Roles().Lookup("tz_literal_reader")
	if err := enginetest.SetObjectACL(e, context.Background(), enginetest.RoleTableRef(t, e, "public.tz_literal_anchor"), authorization.ACLValue{Explicit: true, Entries: []authorization.ACLEntry{{Grantor: e.RoleStore.Published.Root.Read(authorization.PrincipalRef{}).LookupRef("postgres"), Grantee: authorization.ACLGrantee{Role: reader.Ref}, Privileges: authorization.SelectPrivilege}}}); err != nil {
		t.Fatal(err)
	}
	if err := enginetest.SetObjectACL(e, context.Background(), schemaRef(t, e, "pg_catalog"), authorization.ACLValue{Explicit: true}); err != nil {
		t.Fatal(err)
	}
	enginetest.RoleExecute(t, e, op, "SET SESSION AUTHORIZATION tz_literal_reader")
	for _, name := range []string{"timestamptz", "\"timestamptz\"", "timetz", "\"timetz\""} {
		input := "2004-07-01 12:00+00"
		if name == "timetz" || name == "\"timetz\"" {
			input = "05:34:17-05"
		}
		q := "SELECT pg_catalog." + name + " '" + input + "' FROM tz_literal_anchor"
		enginetest.RoleErrorPosition(t, e, op, q, "42501", "permission denied for schema pg_catalog", "", "8")
		_, err := e.Describe(op, enginetest.Statement(t, q))
		enginetest.State(t, err, "42501")
	}
}

func schemaRef(t *testing.T, e *core.Engine, name string) ids.ObjectRef {
	t.Helper()
	v := e.CatalogRead()
	a := v.NameAddress(ids.NameKey{ClassID: ids.NamespaceClassID, Name: name})
	if a == (ids.ObjectAddress{}) {
		t.Fatal("missing namespace", name)
	}
	return e.Catalog.Domain.Ref(v.Epoch(), a)
}
