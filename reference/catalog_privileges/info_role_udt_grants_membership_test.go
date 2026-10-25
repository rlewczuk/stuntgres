package reference_test

import (
	"stuntgres/reference/fixture"
	"testing"
)

func TestReferenceInfoRoleUdtGrantsMembershipVisibility(t *testing.T) {
	t.Run("recursive_paths", func(t *testing.T) {
		ugScenario(t, func(c *ugCase) {
			c.seed()
			c.exec("SET ROLE ug_o", "GRANT USAGE ON TYPE public.ug_t TO ug_r", "RESET ROLE")
			held := c.hold(c.conn, ugQuery)
			row := c.origin("ug_o", "ug_r", "NO")
			c.actor("ug_r", func() { c.check(c.conn, "public", "ug_t", row); c.check(held, "public", "ug_t", row) })
			c.actor("ug_p", func() { c.check(c.conn, "public", "ug_t"); c.check(held, "public", "ug_t") })
			c.exec("GRANT ug_r TO ug_g WITH INHERIT TRUE", "GRANT ug_g TO ug_m WITH INHERIT TRUE", "GRANT ug_r TO ug_x WITH INHERIT TRUE", "GRANT ug_x TO ug_m WITH INHERIT TRUE")
			read := func(want ...[]any) {
				c.actor("ug_m", func() { c.check(c.conn, "public", "ug_t", want...); c.check(held, "public", "ug_t", want...) })
			}
			read(row)
			c.exec("REVOKE ug_g FROM ug_m")
			read(row)
			c.exec("REVOKE ug_x FROM ug_m")
			read()
			c.exec("GRANT ug_r TO ug_m WITH INHERIT FALSE,SET TRUE,ADMIN TRUE")
			read()
			c.actor("ug_m", func() {
				c.exec("SET ROLE ug_r")
				c.check(held, "public", "ug_t", row)
				c.exec("RESET ROLE")
				c.check(held, "public", "ug_t")
			})
			c.exec("GRANT ug_power TO ug_m WITH INHERIT TRUE")
			read()
			c.actor("ug_power", func() { c.check(held, "public", "ug_t", append(c.baseline("ug_o", "public", "ug_t"), row)...) })
			peer := c.peer()
			c.actor("ug_m", func() {
				c.run(peer, fixture.Select("SELECT current_user", []string{"current_user"}, []string{"NAME"}, [][]any{{c.owner}}))
			})
		})
	})
	t.Run("named_grantee_owner_authority", func(t *testing.T) {
		ugScenario(t, func(c *ugCase) {
			c.seed()
			c.exec("SET ROLE ug_o", "GRANT USAGE ON TYPE public.ug_t TO ug_r", "RESET ROLE")
			held := c.hold(c.conn, ugQuery)
			read := func(option string) {
				want := append(c.baseline("ug_o", "public", "ug_t"), c.origin("ug_o", "ug_r", option))
				c.check(c.conn, "public", "ug_t", want...)
				c.check(held, "public", "ug_t", want...)
				c.actor("ug_o", func() { c.check(held, "public", "ug_t", want...) })
			}
			read("NO")
			c.exec("GRANT ug_o TO ug_g WITH INHERIT TRUE", "GRANT ug_g TO ug_r WITH INHERIT TRUE", "GRANT ug_o TO ug_x WITH INHERIT TRUE", "GRANT ug_x TO ug_r WITH INHERIT TRUE")
			read("YES")
			c.exec("REVOKE ug_g FROM ug_r")
			read("YES")
			c.exec("REVOKE ug_x FROM ug_r")
			read("NO")
		})
	})
}
