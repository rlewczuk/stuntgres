package reference_test

import (
	"stuntgres/reference/fixture"
	"testing"
)

func TestReferenceInfoUsagePrivilegesMembership(t *testing.T) {
	t.Run("public_and_enabled_grantor", func(t *testing.T) {
		upScenario(t, func(c *upCase) {
			c.seed()
			held := c.hold(c.conn, upQuery)
			baseline := c.baseline("up_o", "public", "up_t")
			c.actor("up_r", func() { c.check(held, "public", "up_t", c.origin("up_o", "PUBLIC", "NO")) })
			c.actor("up_o", func() { c.check(held, "public", "up_t", baseline...) })
			c.exec("GRANT up_o TO up_m WITH INHERIT TRUE")
			c.actor("up_m", func() { c.check(held, "public", "up_t", baseline...) })
			c.exec("SET ROLE up_o", "REVOKE USAGE ON DOMAIN public.up_t FROM PUBLIC", "GRANT USAGE ON DOMAIN public.up_t TO up_d WITH GRANT OPTION", "RESET ROLE", "SET ROLE up_d", "GRANT USAGE ON DOMAIN public.up_t TO PUBLIC,up_r", "RESET ROLE")
			c.exec("GRANT up_d TO up_x WITH INHERIT TRUE", "GRANT up_r TO up_y WITH INHERIT TRUE")
			c.actor("up_x", func() {
				c.check(held, "public", "up_t", c.origin("up_o", "up_d", "YES"), c.origin("up_d", "PUBLIC", "NO"), c.origin("up_d", "up_r", "NO"))
			})
			c.actor("up_y", func() {
				c.check(held, "public", "up_t", c.origin("up_d", "PUBLIC", "NO"), c.origin("up_d", "up_r", "NO"))
			})
			c.actor("up_p", func() { c.check(held, "public", "up_t", c.origin("up_d", "PUBLIC", "NO")) })
		})
	})
	t.Run("recursive_paths", func(t *testing.T) {
		upScenario(t, func(c *upCase) {
			c.seed()
			c.exec("SET ROLE up_o", "REVOKE USAGE ON DOMAIN public.up_t FROM PUBLIC", "RESET ROLE")
			c.exec("SET ROLE up_o", "GRANT USAGE ON TYPE public.up_t TO up_r", "RESET ROLE")
			held := c.hold(c.conn, upQuery)
			row := c.origin("up_o", "up_r", "NO")
			c.actor("up_r", func() { c.check(c.conn, "public", "up_t", row); c.check(held, "public", "up_t", row) })
			c.actor("up_p", func() { c.check(c.conn, "public", "up_t"); c.check(held, "public", "up_t") })
			c.exec("GRANT up_r TO up_g WITH INHERIT TRUE", "GRANT up_g TO up_m WITH INHERIT TRUE", "GRANT up_r TO up_x WITH INHERIT TRUE", "GRANT up_x TO up_m WITH INHERIT TRUE")
			read := func(want ...[]any) {
				c.actor("up_m", func() { c.check(c.conn, "public", "up_t", want...); c.check(held, "public", "up_t", want...) })
			}
			read(row)
			c.exec("REVOKE up_g FROM up_m")
			read(row)
			c.exec("REVOKE up_x FROM up_m")
			read()
			c.exec("GRANT up_r TO up_m WITH INHERIT FALSE,SET TRUE,ADMIN TRUE")
			read()
			c.actor("up_m", func() {
				c.exec("SET ROLE up_r")
				c.check(held, "public", "up_t", row)
				c.exec("RESET ROLE")
				c.check(held, "public", "up_t")
			})
			c.exec("GRANT up_power TO up_m WITH INHERIT TRUE")
			read()
			c.actor("up_power", func() { c.check(held, "public", "up_t", [][]any{c.origin("up_o", "up_o", "YES"), row}...) })
			peer := c.peer()
			c.actor("up_m", func() {
				c.run(peer, fixture.Select("SELECT current_user", []string{"current_user"}, []string{"NAME"}, [][]any{{c.owner}}))
			})
		})
	})
	t.Run("named_grantee_owner_authority", func(t *testing.T) {
		upScenario(t, func(c *upCase) {
			c.seed()
			c.exec("SET ROLE up_o", "GRANT USAGE ON TYPE public.up_t TO up_r", "RESET ROLE")
			held := c.hold(c.conn, upQuery)
			read := func(option string) {
				want := append(c.baseline("up_o", "public", "up_t"), c.origin("up_o", "up_r", option))
				c.check(c.conn, "public", "up_t", want...)
				c.check(held, "public", "up_t", want...)
				c.actor("up_o", func() { c.check(held, "public", "up_t", want...) })
			}
			read("NO")
			c.exec("GRANT up_o TO up_g WITH INHERIT TRUE", "GRANT up_g TO up_r WITH INHERIT TRUE", "GRANT up_o TO up_x WITH INHERIT TRUE", "GRANT up_x TO up_r WITH INHERIT TRUE")
			read("YES")
			c.exec("REVOKE up_g FROM up_r")
			read("YES")
			c.exec("REVOKE up_x FROM up_r")
			read("NO")
			c.exec("GRANT up_o TO up_r WITH INHERIT FALSE,SET TRUE")
			read("NO")
		})
	})
}
