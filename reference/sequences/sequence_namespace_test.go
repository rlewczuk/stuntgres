//go:build reference

package reference_test

import (
	"stuntgres/reference/fixture"
	"testing"
)

// Native ACL checkpoints use real SetObjectACL in the engine suite. SQL GRANT
// and REVOKE here are oracle fixture setup, not an added native SQL surface.
func TestReferenceSequenceNamespacePrivilegePhases(t *testing.T) {
	p := fixture.NewPair(t)
	c, err := p.Postgres.Conn(p.Ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer c.Close()
	defer func() {
		for _, q := range []string{"RESET SESSION AUTHORIZATION", "DROP SCHEMA private CASCADE", "DROP ROLE seq_namespace_reader"} {
			fixture.SequenceProbeExec(t, p.Ctx, c, q)
		}
	}()
	for _, q := range []string{"CREATE ROLE seq_namespace_reader", "CREATE SCHEMA private", "CREATE SEQUENCE private.s", "REVOKE ALL ON SCHEMA private FROM PUBLIC", "SET SESSION AUTHORIZATION seq_namespace_reader"} {
		fixture.SequenceProbeExec(t, p.Ctx, c, q)
	}
	for _, tc := range []struct{ q, code, message string }{
		{"CREATE SEQUENCE private.denied", "42501", "permission denied for schema private"},
		{"CREATE SEQUENCE private.denied INCREMENT 0", "22023", "INCREMENT must not be zero"},
		{"CREATE SEQUENCE IF NOT EXISTS private.s", "42501", "permission denied for schema private"},
		{"CREATE SEQUENCE IF NOT EXISTS private.s INCREMENT 0", "42501", "permission denied for schema private"},
	} {
		t.Run(tc.q, func(t *testing.T) {
			want := fixture.ControlsReferenceDiagnostic(tc.code, tc.message)
			fixture.SequenceProbeError(t, p.Ctx, c, tc.q, &want)
			stmt, err := c.PrepareContext(p.Ctx, tc.q)
			if err != nil {
				t.Fatal("utility Prepare must succeed", err)
			}
			defer stmt.Close()
			_, err = stmt.ExecContext(p.Ctx)
			fixture.AssertDiagnostic(t, err, &want)
		})
	}
	q := "SELECT nextval('private.s')"
	want := fixture.ControlsReferenceDiagnostic("42501", "permission denied for schema private")
	want.Position = "16"
	fixture.SequenceProbeError(t, p.Ctx, c, q, &want)
	stmt, err := c.PrepareContext(p.Ctx, q)
	if stmt != nil {
		stmt.Close()
	}
	fixture.AssertDiagnostic(t, err, &want)
	fixture.SequenceProbeExec(t, p.Ctx, c, "RESET SESSION AUTHORIZATION")
	fixture.SequenceProbeValue(t, p.Ctx, c, "SELECT nextval('private.s')", 1)
}
