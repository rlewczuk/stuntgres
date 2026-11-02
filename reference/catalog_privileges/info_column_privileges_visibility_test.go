package reference_test

import (
	"testing"

	"stuntgres/reference/fixture"
)

func TestReferenceInfoColumnPrivilegesVisibility(t *testing.T) {
	t.Run("public_and_enabled_endpoints", func(t *testing.T) {
		cpScenario(t, func(c *cpCase) {
			c.seed()
			c.run(c.conn, fixture.Exec("INSERT INTO cp_t VALUES(7,8)", 1))
			c.exec("SET ROLE cp_o", "GRANT SELECT(a) ON cp_t TO PUBLIC", "GRANT UPDATE(b) ON cp_t TO cp_g", "GRANT SELECT(b) ON cp_t TO cp_d WITH GRANT OPTION", "RESET ROLE")
			c.actor("cp_d", func() { c.exec("GRANT SELECT(b) ON cp_t TO cp_r") })
			public := c.row("cp_o", "PUBLIC", "public", "cp_t", "a", "SELECT", "NO")
			incoming := c.row("cp_o", "cp_d", "public", "cp_t", "b", "SELECT", "YES")
			outgoing := c.row("cp_d", "cp_r", "public", "cp_t", "b", "SELECT", "NO")
			c.actor("cp_p", func() {
				c.run(c.conn, fixture.Select("SELECT a FROM cp_t", []string{"a"}, []string{"INT4"}, [][]any{{int64(7)}}))
				c.check(c.conn, "cp_t", "PUBLIC", public)
				c.sibling("cp_t", "PUBLIC")
				c.check(c.conn, "cp_t", "cp_o")
				c.check(c.conn, "cp_t", "cp_g")
				c.check(c.conn, "cp_t", "cp_r")
			})
			c.actor("cp_o", func() {
				c.check(c.conn, "cp_t", "PUBLIC", public)
				c.sibling("cp_t", "PUBLIC", public)
				c.check(c.conn, "cp_t", "cp_d", incoming)
				c.check(c.conn, "cp_t", "cp_r")
			})
			c.actor("cp_d", func() { c.check(c.conn, "cp_t", "cp_d", incoming); c.check(c.conn, "cp_t", "cp_r", outgoing) })
			c.actor("cp_r", func() { c.check(c.conn, "cp_t", "cp_d"); c.check(c.conn, "cp_t", "cp_r", outgoing) })
			c.actor("cp_power", func() { c.check(c.conn, "cp_t", "cp_d", incoming); c.check(c.conn, "cp_t", "cp_r", outgoing) })
			c.exec("GRANT cp_power TO cp_p WITH INHERIT TRUE, SET FALSE")
			c.actor("cp_p", func() {
				c.check(c.conn, "cp_t", "PUBLIC", public)
				c.check(c.conn, "cp_t", "cp_d")
				c.check(c.conn, "cp_t", "cp_r")
			})
		})
	})
	t.Run("recursive_inherit_set_and_admin", func(t *testing.T) {
		cpScenario(t, func(c *cpCase) {
			c.seed()
			c.exec("SET ROLE cp_o", "GRANT SELECT(a) ON cp_t TO cp_g", "RESET ROLE", "GRANT cp_g TO cp_m WITH INHERIT FALSE, SET TRUE")
			group := c.row("cp_o", "cp_g", "public", "cp_t", "a", "SELECT", "NO")
			held := c.hold(c.conn, cpQuery)
			read := func(want ...[]any) {
				c.actor("cp_m", func() { c.check(held, "cp_t", "cp_g", want...); c.check(c.conn, "cp_t", "cp_m") })
			}
			read()
			c.actor("cp_m", func() {
				c.exec("SET ROLE cp_g")
				c.check(held, "cp_t", "cp_g", group)
				c.exec("RESET ROLE")
				c.check(held, "cp_t", "cp_g")
			})
			c.exec("GRANT cp_g TO cp_m WITH ADMIN TRUE, INHERIT FALSE, SET FALSE")
			read()
			c.exec("GRANT cp_x TO cp_m WITH INHERIT TRUE", "GRANT cp_y TO cp_m WITH INHERIT TRUE", "GRANT cp_g TO cp_x WITH INHERIT TRUE", "GRANT cp_g TO cp_y WITH INHERIT TRUE")
			read(group)
			c.exec("REVOKE cp_g FROM cp_x")
			read(group)
			c.exec("REVOKE cp_g FROM cp_y")
			read()
		})
	})
}

func TestReferenceInfoColumnPrivilegesPublicName(t *testing.T) {
	cpScenario(t, func(c *cpCase) {
		c.seed()
		c.exec(`CREATE ROLE "PUBLIC"`, "SET ROLE cp_o", `GRANT SELECT(a) ON cp_t TO "PUBLIC" WITH GRANT OPTION`, "RESET ROLE")
		named := c.row("cp_o", "PUBLIC", "public", "cp_t", "a", "SELECT", "YES")
		pseudo := c.row("cp_o", "PUBLIC", "public", "cp_t", "a", "SELECT", "NO")
		// REL_18_6 compares rolname, not OID=0. The ordinary uppercase role
		// matches visibility but retains named-role grant-option semantics.
		c.actor("cp_p", func() { c.check(c.conn, "cp_t", "PUBLIC", named); c.sibling("cp_t", "PUBLIC") })
		c.exec("SET ROLE cp_o", "GRANT SELECT(a) ON cp_t TO PUBLIC", "RESET ROLE")
		c.actor("cp_p", func() { c.check(c.conn, "cp_t", "PUBLIC", named, pseudo); c.sibling("cp_t", "PUBLIC") })
		c.actor("cp_o", func() { c.check(c.conn, "cp_t", "PUBLIC", named, pseudo); c.sibling("cp_t", "PUBLIC", named, pseudo) })
		c.exec("SET ROLE cp_o", `REVOKE SELECT(a) ON cp_t FROM "PUBLIC"`, "RESET ROLE")
		c.actor("cp_p", func() { c.check(c.conn, "cp_t", "PUBLIC", pseudo); c.sibling("cp_t", "PUBLIC") })
		c.exec("SET ROLE cp_o", "REVOKE SELECT(a) ON cp_t FROM PUBLIC", "RESET ROLE")
		c.actor("cp_p", func() { c.check(c.conn, "cp_t", "PUBLIC"); c.sibling("cp_t", "PUBLIC") })
	})
}
