package reference_test

import (
	"testing"

	"stuntgres/reference/fixture"
)

func TestReferenceInfoUdtPrivilegesMembershipVisibility(t *testing.T) {
	t.Run("recursive_paths_and_role_view", func(t *testing.T) {
		uvScenario(t, func(c *uvCase) {
			c.seed()
			c.exec("SET ROLE uv_o", "GRANT USAGE ON TYPE public.uv_t TO uv_r", "RESET ROLE")
			held, roleHeld := c.hold(c.conn, uvQuery), c.hold(c.conn, uvRoleQuery)
			row := c.origin("uv_o", "uv_r", "NO")
			// PUBLIC is unconditional only in udt_privileges. It does not disclose
			// hidden named origins, nor expand them to the observing member.
			read := func(named ...[]any) {
				want := append([][]any{c.public()}, named...)
				c.check(c.conn, "public", "uv_t", want...)
				c.check(held, "public", "uv_t", want...)
				c.checkQuery(c.conn, uvRoleQuery, "public", "uv_t", named...)
				c.checkQuery(roleHeld, uvRoleQuery, "public", "uv_t", named...)
			}
			c.actor("uv_r", func() { read(row) })
			c.actor("uv_p", func() { read() })
			c.exec("GRANT uv_r TO uv_g WITH INHERIT TRUE", "GRANT uv_g TO uv_m WITH INHERIT TRUE", "GRANT uv_r TO uv_x WITH INHERIT TRUE", "GRANT uv_x TO uv_m WITH INHERIT TRUE")
			member := func(named ...[]any) { c.actor("uv_m", func() { read(named...) }) }
			member(row)
			c.exec("REVOKE uv_g FROM uv_m")
			member(row)
			c.exec("REVOKE uv_x FROM uv_m")
			member()
			c.exec("GRANT uv_r TO uv_m WITH INHERIT FALSE,SET TRUE,ADMIN TRUE")
			member()
			c.actor("uv_m", func() {
				c.exec("SET ROLE uv_r")
				read(row)
				c.exec("RESET ROLE")
				read()
			})
			c.exec("GRANT uv_power TO uv_m WITH INHERIT TRUE")
			member()
			c.actor("uv_power", func() {
				want := append(c.baseline("uv_o", "public", "uv_t"), row)
				c.check(c.conn, "public", "uv_t", want...)
				c.check(held, "public", "uv_t", want...)
				c.checkQuery(roleHeld, uvRoleQuery, "public", "uv_t", want...)
			})
			peer := c.peer()
			c.actor("uv_m", func() {
				c.run(peer, fixture.Select("SELECT current_user", []string{"current_user"}, []string{"NAME"}, [][]any{{c.owner}}))
			})
		})
	})
	t.Run("grantee_owner_authority_not_observer_authority", func(t *testing.T) {
		uvScenario(t, func(c *uvCase) {
			c.seed()
			c.exec("SET ROLE uv_o", "GRANT USAGE ON TYPE public.uv_t TO uv_r", "RESET ROLE")
			held := c.hold(c.conn, uvQuery)
			read := func(option string) {
				want := append(c.baseline("uv_o", "public", "uv_t"), c.origin("uv_o", "uv_r", option))
				c.check(c.conn, "public", "uv_t", want...)
				c.check(held, "public", "uv_t", want...)
				c.actor("uv_o", func() { c.check(held, "public", "uv_t", want...) })
				c.actor("uv_r", func() { c.check(held, "public", "uv_t", c.public(), c.origin("uv_o", "uv_r", option)) })
			}
			read("NO")
			c.exec("GRANT uv_o TO uv_g WITH INHERIT TRUE", "GRANT uv_g TO uv_r WITH INHERIT TRUE", "GRANT uv_o TO uv_x WITH INHERIT TRUE", "GRANT uv_x TO uv_r WITH INHERIT TRUE")
			// uv_r now enables the owner too, so the full bag is visible.
			ownerEnabled := func() {
				want := append(c.baseline("uv_o", "public", "uv_t"), c.origin("uv_o", "uv_r", "YES"))
				c.check(c.conn, "public", "uv_t", want...)
				c.check(held, "public", "uv_t", want...)
				c.actor("uv_r", func() { c.check(c.conn, "public", "uv_t", want...); c.check(held, "public", "uv_t", want...) })
			}
			ownerEnabled()
			c.exec("REVOKE uv_g FROM uv_r")
			ownerEnabled()
			c.exec("REVOKE uv_x FROM uv_r")
			read("NO")
		})
	})
}
