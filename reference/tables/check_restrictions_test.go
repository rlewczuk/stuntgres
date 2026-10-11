//go:build reference

package reference_test

import (
	"stuntgres/reference/fixture"
	"testing"
)

// The remaining PostgreSQL-only control requires SRF execution.
func TestReferenceCheckExcludedExpressionControlsPostgres(t *testing.T) {
	for _, tc := range []struct{ query, code, message string }{
		{"CREATE TABLE t(a INT CHECK(generate_series(1,2)>0))", "0A000", "set-returning functions are not allowed in check constraints"},
	} {
		t.Run(tc.query, func(t *testing.T) {
			p := fixture.NewPair(t)
			fixture.RunStep(t, p.Ctx, p.Postgres, p.PostgresRecord, fixture.ImplicitError(tc.query, fixture.Diagnostic{Code: tc.code, Message: tc.message, Position: "28"}))
			fixture.RunStep(t, p.Ctx, p.Postgres, p.PostgresRecord, fixture.ImplicitError("SELECT * FROM t", fixture.Diagnostic{Code: "42P01", Message: `relation "t" does not exist`, Position: "15"}))
		})
	}
}
