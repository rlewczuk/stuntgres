package engine

import (
	"context"
	"errors"
	"reflect"
	"stuntgres/internal/sqlstate"
	"testing"
)

// Denied-schema setup exercises the private catalog ACL seam: native has no
// public GRANT/REVOKE API. The missing-schema public route is migrated to
// TestReferenceLikeLegacyMissingNamespace; PostgreSQL denied-schema SQL is
// independently covered by TestReferenceLikeOracleSecurity.
func TestLikeNamespaceDiagnostics(t *testing.T) {
	e := New()
	op, done := roleOperation(t, e, "postgres")
	defer done()
	roleExecute(t, e, op, "CREATE USER like_reader")
	roleExecute(t, e, op, "CREATE TABLE t(v TEXT)")
	roleExecute(t, e, op, "CREATE SCHEMA like_denied")
	if err := e.SetObjectACL(context.Background(), schemaRef(t, e, "like_denied"), ACLValue{Explicit: true}); err != nil {
		t.Fatal(err)
	}
	for _, tc := range []struct{ query, code, message string }{
		{"SELECT v OPERATOR(like_denied.~~) 'a%' FROM public.t", "42501", "permission denied for schema like_denied"},
	} {
		if tc.code == "42501" {
			roleExecute(t, e, op, "SET SESSION AUTHORIZATION like_reader")
		}
		want := &sqlstate.Error{Severity: "ERROR", SeverityUnlocalized: "ERROR", Code: tc.code, Message: tc.message, Position: "10"}
		_, err := e.Describe(op, statement(t, tc.query))
		var got *sqlstate.Error
		if !errors.As(err, &got) || !reflect.DeepEqual(got, want) {
			t.Fatalf("Prepare %s: %#v want %#v", tc.query, err, want)
		}
		roleErrorPosition(t, e, op, tc.query, tc.code, tc.message, "", "10")
	}
}
