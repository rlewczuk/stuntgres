package reference_test

import (
	"testing"

	"stuntgres/reference/fixture"
)

func TestReferenceInfoCollationsDiagnostics(t *testing.T) {
	icoScenario(t, false, func(c *icoCase) {
		want := fixture.ControlsReferenceDiagnostic("42703", `column "missing" does not exist`)
		want.Position = "8"
		for _, suffix := range []string{"", " WHERE FALSE"} {
			for _, prepared := range []bool{false,true} {
				s := fixture.ErrorDiagnostic("SELECT missing FROM information_schema.collations"+suffix, want)
				s.SelectRows, s.Prepared, s.ErrorStage = true, prepared, "execute"
				if prepared { s.ErrorStage = "prepare" }
				c.run(c.conn, s)
			}
		}
		c.routes(c.projection(icoQuery+" WHERE "+icoKnown+" ORDER BY collation_name", c.seeds()))
	})
}
