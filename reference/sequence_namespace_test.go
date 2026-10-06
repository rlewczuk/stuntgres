//go:build reference

package reference_test

import "testing"

// Native ACL checkpoints use real SetObjectACL in the engine suite. SQL GRANT
// and REVOKE here are oracle fixture setup, not an added native SQL surface.
func TestReferenceSequenceNamespacePrivilegePhases(t *testing.T) {
	p := newReferencePair(t)
	c, err := p.postgres.Conn(p.ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer c.Close()
	defer func() {
		for _, q := range []string{"RESET SESSION AUTHORIZATION", "DROP SCHEMA private CASCADE", "DROP ROLE seq_namespace_reader"} {
			sequenceProbeExec(t, p.ctx, c, q)
		}
	}()
	for _, q := range []string{"CREATE ROLE seq_namespace_reader", "CREATE SCHEMA private", "CREATE SEQUENCE private.s", "REVOKE ALL ON SCHEMA private FROM PUBLIC", "SET SESSION AUTHORIZATION seq_namespace_reader"} {
		sequenceProbeExec(t, p.ctx, c, q)
	}
	for _, tc := range []struct{ q, code, message string }{
		{"CREATE SEQUENCE private.denied", "42501", "permission denied for schema private"},
		{"CREATE SEQUENCE private.denied INCREMENT 0", "22023", "INCREMENT must not be zero"},
		{"CREATE SEQUENCE IF NOT EXISTS private.s", "42501", "permission denied for schema private"},
		{"CREATE SEQUENCE IF NOT EXISTS private.s INCREMENT 0", "42501", "permission denied for schema private"},
	} {
		t.Run(tc.q, func(t *testing.T) {
			want := controlsReferenceDiagnostic(tc.code, tc.message)
			sequenceProbeError(t, p.ctx, c, tc.q, &want)
			stmt, err := c.PrepareContext(p.ctx, tc.q)
			if err != nil {
				t.Fatal("utility Prepare must succeed", err)
			}
			defer stmt.Close()
			_, err = stmt.ExecContext(p.ctx)
			referenceAssertDiagnostic(t, err, &want)
		})
	}
	q := "SELECT nextval('private.s')"
	want := controlsReferenceDiagnostic("42501", "permission denied for schema private")
	want.Position = "16"
	sequenceProbeError(t, p.ctx, c, q, &want)
	stmt, err := c.PrepareContext(p.ctx, q)
	if stmt != nil {
		stmt.Close()
	}
	referenceAssertDiagnostic(t, err, &want)
	sequenceProbeExec(t, p.ctx, c, "RESET SESSION AUTHORIZATION")
	sequenceProbeValue(t, p.ctx, c, "SELECT nextval('private.s')", 1)
}
