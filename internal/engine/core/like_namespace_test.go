package core_test

import (
	"context"
	"errors"
	"reflect"
	"testing"

	"stuntgres/internal/engine"
	"stuntgres/internal/engine/authorization"
	"stuntgres/internal/engine/enginetest"
	"stuntgres/internal/sqlstate"
)

// Denied-schema setup exercises the private catalog ACL seam: native has no
// public GRANT/REVOKE API. The missing-schema public route is migrated to
// TestReferenceLikeLegacyMissingNamespace; PostgreSQL denied-schema SQL is
// independently covered by TestReferenceLikeOracleSecurity.
func TestLikeNamespaceDiagnostics(t *testing.T) {
	e := engine.New()
	op, done := enginetest.RoleOperation(t, e, "postgres")
	defer done()
	enginetest.RoleExecute(t, e, op, "CREATE USER like_reader")
	enginetest.RoleExecute(t, e, op, "CREATE TABLE t(v TEXT)")
	enginetest.RoleExecute(t, e, op, "CREATE SCHEMA like_denied")
	if err := enginetest.SetObjectACL(e, context.Background(), schemaRef(t, e, "like_denied"), authorization.ACLValue{Explicit: true}); err != nil {
		t.Fatal(err)
	}
	for _, tc := range []struct{ query, code, message string }{
		{"SELECT v OPERATOR(like_denied.~~) 'a%' FROM public.t", "42501", "permission denied for schema like_denied"},
	} {
		if tc.code == "42501" {
			enginetest.RoleExecute(t, e, op, "SET SESSION AUTHORIZATION like_reader")
		}
		want := &sqlstate.Error{Severity: "ERROR", SeverityUnlocalized: "ERROR", Code: tc.code, Message: tc.message, Position: "10"}
		_, err := e.Describe(op, enginetest.Statement(t, tc.query))
		var got *sqlstate.Error
		if !errors.As(err, &got) || !reflect.DeepEqual(got, want) {
			t.Fatalf("Prepare %s: %#v want %#v", tc.query, err, want)
		}
		enginetest.RoleErrorPosition(t, e, op, tc.query, tc.code, tc.message, "", "10")
	}
}
