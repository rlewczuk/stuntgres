package reference_test

import "testing"

func TestReferenceInfoColumnPrivilegesOrigins(t *testing.T) {
	t.Run("raw_union_and_owner_closure", func(t *testing.T) {
		cpScenario(t, func(c *cpCase) {
			c.seed()
			c.exec("SET ROLE cp_o", "GRANT SELECT ON cp_t TO cp_r", "GRANT SELECT(a) ON cp_t TO cp_r", "RESET ROLE")
			c.check(c.conn, "cp_t", "cp_r", c.r("a", "SELECT", "NO"), c.r("b", "SELECT", "NO"))
			c.exec("SET ROLE cp_o", "GRANT SELECT(a) ON cp_t TO cp_r WITH GRANT OPTION", "RESET ROLE")
			c.check(c.conn, "cp_t", "cp_r", c.r("a", "SELECT", "NO"), c.r("a", "SELECT", "YES"), c.r("b", "SELECT", "NO"))
			c.exec("GRANT cp_o TO cp_r WITH INHERIT TRUE")
			// UNION precedes owner grantability: two equal final YES rows survive.
			c.check(c.conn, "cp_t", "cp_r", c.r("a", "SELECT", "YES"), c.r("a", "SELECT", "YES"), c.r("b", "SELECT", "YES"))
			c.exec("REVOKE cp_o FROM cp_r", "SET ROLE cp_o", "REVOKE GRANT OPTION FOR SELECT(a) ON cp_t FROM cp_r", "RESET ROLE")
			c.check(c.conn, "cp_t", "cp_r", c.r("a", "SELECT", "NO"), c.r("b", "SELECT", "NO"))
			c.exec("SET ROLE cp_o", "REVOKE SELECT(a) ON cp_t FROM cp_r", "RESET ROLE")
			c.check(c.conn, "cp_t", "cp_r", c.r("a", "SELECT", "NO"), c.r("b", "SELECT", "NO"))
			c.exec("SET ROLE cp_o", "REVOKE SELECT ON cp_t FROM cp_r", "RESET ROLE")
			c.check(c.conn, "cp_t", "cp_r")
		})
	})
	t.Run("four_masks_and_absent_versus_empty", func(t *testing.T) {
		cpScenario(t, func(c *cpCase) {
			c.seed()
			c.check(c.conn, "cp_t", "cp_o", c.baseline("cp_o", "cp_t", "a", "b")...)
			for _, privilege := range []string{"INSERT", "SELECT", "UPDATE", "REFERENCES"} {
				c.exec("SET ROLE cp_o", "GRANT "+privilege+" ON cp_t TO cp_r", "GRANT "+privilege+"(a) ON cp_t TO cp_r WITH GRANT OPTION", "RESET ROLE")
				c.check(c.conn, "cp_t", "cp_r", c.r("a", privilege, "NO"), c.r("a", privilege, "YES"), c.r("b", privilege, "NO"))
				c.exec("SET ROLE cp_o", "REVOKE GRANT OPTION FOR "+privilege+"(a) ON cp_t FROM cp_r", "RESET ROLE")
				c.check(c.conn, "cp_t", "cp_r", c.r("a", privilege, "NO"), c.r("b", privilege, "NO"))
				// A table revoke removes matching column rights as well.
				c.exec("SET ROLE cp_o", "REVOKE "+privilege+" ON cp_t FROM cp_r", "RESET ROLE")
				c.check(c.conn, "cp_t", "cp_r")
				c.exec("SET ROLE cp_o", "GRANT "+privilege+"(b) ON cp_t TO cp_r", "RESET ROLE")
				c.check(c.conn, "cp_t", "cp_r", c.r("b", privilege, "NO"))
				c.exec("SET ROLE cp_o", "REVOKE "+privilege+"(b) ON cp_t FROM cp_r", "RESET ROLE")
				c.check(c.conn, "cp_t", "cp_r")
			}
			c.exec("SET ROLE cp_o", "REVOKE ALL PRIVILEGES ON cp_t FROM cp_o", "RESET ROLE")
			c.check(c.conn, "cp_t", "cp_o")
			c.check(c.conn, "cp_t", "cp_r")
		})
	})
	t.Run("distinct_grantors_not_effective_members", func(t *testing.T) {
		cpScenario(t, func(c *cpCase) {
			c.seed()
			c.exec("SET ROLE cp_o", "GRANT SELECT(a) ON cp_t TO cp_d,cp_e WITH GRANT OPTION", "RESET ROLE")
			c.actor("cp_d", func() { c.exec("GRANT SELECT(a) ON cp_t TO cp_r") })
			c.actor("cp_e", func() { c.exec("GRANT SELECT(a) ON cp_t TO cp_r") })
			first := c.row("cp_d", "cp_r", "public", "cp_t", "a", "SELECT", "NO")
			second := c.row("cp_e", "cp_r", "public", "cp_t", "a", "SELECT", "NO")
			c.check(c.conn, "cp_t", "cp_r", first, second)
			c.exec("GRANT cp_r TO cp_m WITH INHERIT TRUE")
			c.actor("cp_m", func() {
				c.check(c.conn, "cp_t", "cp_r", first, second)
				c.check(c.conn, "cp_t", "cp_m")
			})
			c.actor("cp_d", func() { c.check(c.conn, "cp_t", "cp_r", first) })
			c.actor("cp_o", func() { c.check(c.conn, "cp_t", "cp_r") })
		})
	})
}
