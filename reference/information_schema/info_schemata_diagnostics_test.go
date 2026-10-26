package reference_test

import (
	"stuntgres/reference/fixture"
	"testing"
)

func iscMissing(c *iscCase) {
	c.t.Helper()
	want := fixture.ControlsReferenceDiagnostic("42703", `column "missing" does not exist`)
	want.Position = "8"
	for _, suffix := range []string{"", " WHERE FALSE"} {
		for _, prepared := range []bool{false, true} {
			s := fixture.ErrorDiagnostic("SELECT missing FROM information_schema.schemata"+suffix, want)
			s.SelectRows, s.Prepared, s.ErrorStage = true, prepared, "execute"
			if prepared {
				s.ErrorStage = "prepare"
			}
			c.run(c.conn, s)
			c.routes(c.projection(iscQuery+" WHERE schema_name='isc_owned'", c.row("isc_owned", "isc_owner")))
		}
	}
}
func TestReferenceInfoSchemataDiagnostics(t *testing.T) {
	iscScenario(t, func(c *iscCase) {
		c.exec("CREATE ROLE isc_owner", "CREATE SCHEMA isc_owned AUTHORIZATION isc_owner")
		iscMissing(c)
		c.routes(c.projection(iscQuery+" WHERE schema_name='isc_absent'", nil))
	})
}
