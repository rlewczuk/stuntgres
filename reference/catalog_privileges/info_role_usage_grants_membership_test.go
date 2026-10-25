package reference_test

import (
	"stuntgres/reference/fixture"
	"testing"
)

func TestReferenceInfoRoleUsageGrantsMembership(t *testing.T) {
	t.Run("public_and_enabled_grantor", func(t *testing.T) {
		rugScenario(t, func(c *rugCase) {
			c.seed()
			held := c.hold(c.conn, rugQuery)
			baseline := c.baseline("rug_o", "public", "rug_t")
			c.actor("rug_r", func() { c.check(held, "public", "rug_t") })
			c.actor("rug_o", func() { c.check(held, "public", "rug_t", baseline...) })
			c.exec("GRANT rug_o TO rug_m WITH INHERIT TRUE")
			c.actor("rug_m", func() { c.check(held, "public", "rug_t", baseline...) })
			c.exec("SET ROLE rug_o", "REVOKE USAGE ON DOMAIN public.rug_t FROM PUBLIC", "GRANT USAGE ON DOMAIN public.rug_t TO rug_d WITH GRANT OPTION", "RESET ROLE", "SET ROLE rug_d", "GRANT USAGE ON DOMAIN public.rug_t TO PUBLIC,rug_r", "RESET ROLE")
			c.exec("GRANT rug_d TO rug_x WITH INHERIT TRUE", "GRANT rug_r TO rug_y WITH INHERIT TRUE")
			c.actor("rug_x", func() { c.check(held, "public", "rug_t", c.origin("rug_o", "rug_d", "YES"), c.origin("rug_d", "PUBLIC", "NO"), c.origin("rug_d", "rug_r", "NO")) })
			c.actor("rug_y", func() { c.check(held, "public", "rug_t", c.origin("rug_d", "rug_r", "NO")) })
			c.actor("rug_p", func() { c.check(held, "public", "rug_t") })
		})
	})
	t.Run("recursive_paths", func(t *testing.T) {
		rugScenario(t, func(c *rugCase) {
			c.seed()
			c.exec("SET ROLE rug_o", "GRANT USAGE ON TYPE public.rug_t TO rug_r", "RESET ROLE")
			held := c.hold(c.conn, rugQuery)
			row := c.origin("rug_o", "rug_r", "NO")
			c.actor("rug_r", func() { c.check(c.conn, "public", "rug_t", row); c.check(held, "public", "rug_t", row) })
			c.actor("rug_p", func() { c.check(c.conn, "public", "rug_t"); c.check(held, "public", "rug_t") })
			c.exec("GRANT rug_r TO rug_g WITH INHERIT TRUE", "GRANT rug_g TO rug_m WITH INHERIT TRUE", "GRANT rug_r TO rug_x WITH INHERIT TRUE", "GRANT rug_x TO rug_m WITH INHERIT TRUE")
			read := func(want ...[]any) {
				c.actor("rug_m", func() { c.check(c.conn, "public", "rug_t", want...); c.check(held, "public", "rug_t", want...) })
			}
			read(row)
			c.exec("REVOKE rug_g FROM rug_m")
			read(row)
			c.exec("REVOKE rug_x FROM rug_m")
			read()
			c.exec("GRANT rug_r TO rug_m WITH INHERIT FALSE,SET TRUE,ADMIN TRUE")
			read()
			c.actor("rug_m", func() {
				c.exec("SET ROLE rug_r")
				c.check(held, "public", "rug_t", row)
				c.exec("RESET ROLE")
				c.check(held, "public", "rug_t")
			})
			c.exec("GRANT rug_power TO rug_m WITH INHERIT TRUE")
			read()
			c.actor("rug_power", func() { c.check(held, "public", "rug_t", append(c.baseline("rug_o", "public", "rug_t"), row)...) })
			peer := c.peer()
			c.actor("rug_m", func() {
				c.run(peer, fixture.Select("SELECT current_user", []string{"current_user"}, []string{"NAME"}, [][]any{{c.owner}}))
			})
		})
	})
	t.Run("named_grantee_owner_authority", func(t *testing.T) {
		rugScenario(t, func(c *rugCase) {
			c.seed()
			c.exec("SET ROLE rug_o", "GRANT USAGE ON TYPE public.rug_t TO rug_r", "RESET ROLE")
			held := c.hold(c.conn, rugQuery)
			read := func(option string) {
				want := append(c.baseline("rug_o", "public", "rug_t"), c.origin("rug_o", "rug_r", option))
				c.check(c.conn, "public", "rug_t", want...)
				c.check(held, "public", "rug_t", want...)
				c.actor("rug_o", func() { c.check(held, "public", "rug_t", want...) })
			}
			read("NO")
			c.exec("GRANT rug_o TO rug_g WITH INHERIT TRUE", "GRANT rug_g TO rug_r WITH INHERIT TRUE", "GRANT rug_o TO rug_x WITH INHERIT TRUE", "GRANT rug_x TO rug_r WITH INHERIT TRUE")
			read("YES")
			c.exec("REVOKE rug_g FROM rug_r")
			read("YES")
			c.exec("REVOKE rug_x FROM rug_r")
			read("NO")
			c.exec("GRANT rug_o TO rug_r WITH INHERIT FALSE,SET TRUE")
			read("NO")
		})
	})
}
