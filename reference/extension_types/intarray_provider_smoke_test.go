package reference_test

import (
	"database/sql"
	"testing"

	"stuntgres/reference/fixture"
)

func TestReferenceIntarrayProviderFeatureSmoke(t *testing.T) {
	intarrayScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
		// The integer arrays predate installation and retain builtin metadata,
		// including ordinary NULL elements that owning leaf operations may reject.
		intarraySeedControls(t, p, b, c)
		intarrayDeclarationChecks(t, p, b, c, intarrayFixtureOwner(b))
		intarrayArrayReads(t, p, b, c)
		intarraySupportReads(t, p, b, c)
		intarrayBoth(t, p, b, c, intarraySelect(b, "SELECT $1::ia_provider.intbig_gkey AS k", []string{"k"}, []string{"INTBIG_GKEY"}, nil, [][]any{{nil}}, nil))
		for _, prepared := range []bool{false, true} {
			intarrayError(t, p, b, c, "SELECT $1::ia_provider.intbig_gkey", prepared, "execute", intarrayParameterReject("x"), "x")
		}
		if !b.PG {
			intarrayNativeControls(t, p, b, c)
		}
		for _, q := range []string{"DROP VIEW ia_support_io", "DROP TABLE ia_support", "DROP TABLE ia_queries", "DROP EXTENSION intarray"} {
			fixture.RunStep(t, p.Ctx, c, b.Recorder, fixture.Exec(q, 0))
		}
		intarrayAbsent(t, p, b, c)
		intarrayArrayReads(t, p, b, c)
	})
}
