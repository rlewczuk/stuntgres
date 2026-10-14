package reference_test

import (
	"testing"

	"stuntgres/reference/fixture"
)

func TestReferenceSeriesCheckRestrictions(t *testing.T) {
	p := fixture.NewScenario(t)
	for _, b := range p.Backends() {
		t.Run(b.Name, func(t *testing.T) {
			for _, prepared := range []bool{false, true} {
				s := fixture.ImplicitError("CREATE TABLE t(a INT CHECK(generate_series(1,2)>0))", fixture.Diagnostic{Code: "0A000", Message: "set-returning functions are not allowed in check constraints", Position: "28"})
				s.Prepared = prepared
				s.ErrorStage = "execute"
				fixture.RunStep(t, p.Ctx, b.DB, b.Recorder, s)
				fixture.RunStep(t, p.Ctx, b.DB, b.Recorder, fixture.ImplicitError("SELECT * FROM t", fixture.Diagnostic{Code: "42P01", Message: `relation "t" does not exist`, Position: "15"}))
			}
		})
	}
}
