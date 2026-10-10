//go:build reference

package reference_test

import "testing"

// The remaining PostgreSQL-only control requires SRF execution.
func TestReferenceCheckExcludedExpressionControlsPostgres(t *testing.T) {
	for _, tc := range []struct{ query, code, message string }{
		{"CREATE TABLE t(a INT CHECK(generate_series(1,2)>0))", "0A000", "set-returning functions are not allowed in check constraints"},
	} {
		t.Run(tc.query, func(t *testing.T) {
			p := newReferencePair(t)
			referenceRunStep(t, p.ctx, p.postgres, p.postgresRecord, referenceImplicitError(tc.query, referenceDiagnostic{Code: tc.code, Message: tc.message, Position: "28"}))
			referenceRunStep(t, p.ctx, p.postgres, p.postgresRecord, referenceImplicitError("SELECT * FROM t", referenceDiagnostic{Code: "42P01", Message: `relation "t" does not exist`, Position: "15"}))
		})
	}
}
