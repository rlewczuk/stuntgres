package reference_test

import (
	"stuntgres/reference/fixture"
	"testing"
)

func umoMissingColumn(c *umoCase) {
	c.t.Helper()
	d := fixture.ControlsReferenceDiagnostic("42703", `column "missing" does not exist`)
	d.Position = "8"
	for _, prepared := range []bool{false, true} {
		s := fixture.ErrorDiagnostic("SELECT missing FROM information_schema.user_mapping_options", d)
		s.SelectRows, s.Prepared, s.ErrorStage = true, prepared, "execute"
		if prepared {
			s.ErrorStage = "prepare"
		}
		c.run(s)
	}
}

func TestReferenceInfoUserMappingOptionsDiagnostics(t *testing.T) {
	umoScenario(t, func(c *umoCase) {
		umoMissingColumn(c)
		c.seed()
		failures := []struct{ query, state, message string }{
			{"CREATE USER MAPPING FOR umo_denied SERVER umo_server OPTIONS(x 'one',x 'two')", "42710", `option "x" provided more than once`},
			{"ALTER USER MAPPING FOR umo_alice SERVER umo_server OPTIONS(ADD password 'duplicate')", "42710", `option "password" provided more than once`},
			{"ALTER USER MAPPING FOR umo_alice SERVER umo_server OPTIONS(SET x 'absent')", "42704", `option "x" not found`},
			{"ALTER USER MAPPING FOR umo_alice SERVER umo_server OPTIONS(DROP x)", "42704", `option "x" not found`},
			{"ALTER USER MAPPING FOR umo_alice SERVER umo_server OPTIONS(SET password 'not published',DROP x)", "42704", `option "x" not found`},
		}
		for _, f := range failures {
			for _, prepared := range []bool{false, true} {
				s := fixture.ErrorDiagnostic(f.query, fixture.ControlsReferenceDiagnostic(f.state, f.message))
				s.Prepared, s.ErrorStage = prepared, "execute"
				c.run(s)
				c.check(c.rows("all"))
			}
		}
		for _, tc := range []struct{ role, query, message string }{
			{"umo_alice", "ALTER USER MAPPING FOR umo_alice SERVER umo_server OPTIONS(SET password 'forbidden')", "permission denied for foreign server umo_server"},
			{"umo_reader", "ALTER USER MAPPING FOR umo_bob SERVER umo_server OPTIONS(SET password 'forbidden')", "must be owner of foreign server umo_server"},
		} {
			if tc.role == "umo_reader" {
				c.exec("GRANT USAGE ON FOREIGN SERVER umo_server TO umo_reader")
			}
			c.exec("SET SESSION AUTHORIZATION " + tc.role)
			for _, prepared := range []bool{false, true} {
				s := fixture.ErrorDiagnostic(tc.query, fixture.ControlsReferenceDiagnostic("42501", tc.message))
				s.Prepared, s.ErrorStage = prepared, "execute"
				c.run(s)
				c.exec("RESET SESSION AUTHORIZATION")
				c.check(c.rows("all"))
				c.exec("SET SESSION AUTHORIZATION " + tc.role)
			}
			c.exec("RESET SESSION AUTHORIZATION")
		}
		c.exec("SET SESSION AUTHORIZATION umo_denied")
		c.check(nil)
		for _, prepared := range []bool{false, true} {
			s := fixture.ErrorDiagnostic("SELECT * FROM pg_catalog.pg_user_mapping", fixture.ControlsReferenceDiagnostic("42501", "permission denied for table pg_user_mapping"))
			s.SelectRows, s.Prepared, s.ErrorStage = true, prepared, "execute"
			c.run(s)
		}
		c.exec("RESET SESSION AUTHORIZATION")
		c.check(c.rows("all"))
	})
}
