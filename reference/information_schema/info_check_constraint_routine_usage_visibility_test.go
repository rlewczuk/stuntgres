package reference_test

import (
	"testing"

	"stuntgres/reference/fixture"
)

func TestReferenceInfoCheckConstraintRoutineUsageVisibility(t *testing.T) {
	ccruScenario(t, func(c *ccruCase) {
		c.exec("CREATE ROLE ccru_o", "CREATE ROLE ccru_r", "CREATE ROLE ccru_i", "CREATE ROLE ccru_s", "CREATE ROLE ccru_t", "CREATE ROLE ccru_power SUPERUSER", "GRANT ccru_o TO ccru_i WITH INHERIT TRUE", "GRANT ccru_o TO ccru_s WITH INHERIT FALSE, SET TRUE", "CREATE SCHEMA ccru_private", "REVOKE ALL ON SCHEMA ccru_private FROM PUBLIC", "CREATE FUNCTION ccru_private.ccru_f(integer) RETURNS boolean RETURN $1 > 0", "CREATE TABLE ccru_private.ccru_t(v integer CONSTRAINT ccru_visible CHECK(ccru_private.ccru_f(v)))", "ALTER FUNCTION ccru_private.ccru_f(integer) OWNER TO ccru_o", "ALTER TABLE ccru_private.ccru_t OWNER TO ccru_t", "REVOKE ALL ON FUNCTION ccru_private.ccru_f(integer) FROM PUBLIC")
		f := c.identity("ccru_private", "ccru_f", "integer")
		want := [][]any{c.row("ccru_private", "ccru_visible", "ccru_private", f)}
		held := c.hold(ccruQuery)
		check := func(rows [][]any) {
			c.read(rows...)
			held(c.step(ccruQuery, rows))
		}
		for _, actor := range []struct {
			role    string
			visible bool
		}{{"ccru_o", true}, {"ccru_i", true}, {"ccru_s", false}, {"ccru_r", false}, {"ccru_t", false}, {"ccru_power", true}} {
			c.exec("SET ROLE " + actor.role)
			var rows [][]any
			if actor.visible {
				rows = want
			}
			check(rows)
			c.exec("RESET ROLE")
		}
		c.exec("SET ROLE ccru_s")
		check(nil)
		c.exec("SET ROLE ccru_o")
		check(want)
		c.exec("RESET ROLE", "SET ROLE ccru_s")
		check(nil)
		c.exec("RESET ROLE", "GRANT USAGE ON SCHEMA ccru_private TO ccru_r", "GRANT SELECT ON ccru_private.ccru_t TO ccru_r", "GRANT EXECUTE ON FUNCTION ccru_private.ccru_f(integer) TO ccru_r", "SET ROLE ccru_r")
		check(nil)
		c.exec("RESET ROLE", "REVOKE USAGE ON SCHEMA ccru_private FROM ccru_r", "REVOKE SELECT ON ccru_private.ccru_t FROM ccru_r", "REVOKE EXECUTE ON FUNCTION ccru_private.ccru_f(integer) FROM ccru_r", "SET ROLE ccru_r")
		check(nil)
		c.exec("RESET ROLE", "GRANT ccru_o TO ccru_r WITH INHERIT TRUE", "SET ROLE ccru_r")
		check(want)
		c.exec("RESET ROLE", "REVOKE ccru_o FROM ccru_r", "SET ROLE ccru_r")
		check(nil)
		c.exec("RESET ROLE", "ALTER FUNCTION ccru_private.ccru_f(integer) OWNER TO ccru_r", "SET ROLE ccru_o")
		check(nil)
		c.exec("RESET ROLE", "SET ROLE ccru_r")
		check(want)
		c.exec("RESET ROLE")
		c.run(fixture.Select("SELECT pg_has_role('ccru_o','USAGE') AS owner_authority", []string{"owner_authority"}, []string{"BOOL"}, [][]any{{true}}))
	})
}
