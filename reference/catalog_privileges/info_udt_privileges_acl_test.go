package reference_test

import "testing"

func TestReferenceInfoUdtPrivilegesACLTransitions(t *testing.T) {
	t.Run("options_public_and_empty", func(t *testing.T) {
		uvScenario(t, func(c *uvCase) {
			c.seed()
			held := c.hold(c.conn, uvQuery)
			read := func(want ...[]any) {
				c.check(c.conn, "public", "uv_t", want...)
				c.check(held, "public", "uv_t", want...)
			}
			unrelated := func(want ...[]any) { c.actor("uv_m", func() { read(want...) }) }
			c.acl("absent")
			read(c.baseline("uv_o", "public", "uv_t")...)
			unrelated(c.public())
			c.exec("SET ROLE uv_o", "REVOKE USAGE ON TYPE public.uv_t FROM PUBLIC", "RESET ROLE")
			own := c.origin("uv_o", "uv_o", "YES")
			c.acl("nonempty")
			read(own)
			unrelated()
			for _, tc := range []struct{ sql, option string }{
				{"GRANT USAGE ON TYPE public.uv_t TO uv_r", "NO"},
				{"GRANT USAGE ON TYPE public.uv_t TO uv_r WITH GRANT OPTION", "YES"},
				{"REVOKE GRANT OPTION FOR USAGE ON TYPE public.uv_t FROM uv_r", "NO"},
			} {
				c.exec("SET ROLE uv_o", tc.sql, "RESET ROLE")
				read(own, c.origin("uv_o", "uv_r", tc.option))
				unrelated()
			}
			c.exec("SET ROLE uv_o", "GRANT USAGE ON TYPE public.uv_t TO PUBLIC", "RESET ROLE")
			read(own, c.origin("uv_o", "uv_r", "NO"), c.public())
			unrelated(c.public())
			c.exec("SET ROLE uv_o", "REVOKE USAGE ON TYPE public.uv_t FROM PUBLIC", "REVOKE USAGE ON TYPE public.uv_t FROM uv_r", "RESET ROLE")
			read(own)
			unrelated()
			c.exec("SET ROLE uv_o", "REVOKE USAGE ON TYPE public.uv_t FROM uv_o", "RESET ROLE")
			c.acl("empty")
			read()
			c.actor("uv_o", func() { read() })
			unrelated()
		})
	})
	t.Run("distinct_grantor_origins", func(t *testing.T) {
		uvScenario(t, func(c *uvCase) {
			c.seed()
			c.exec("SET ROLE uv_o", "GRANT USAGE ON TYPE public.uv_t TO uv_r WITH GRANT OPTION", "RESET ROLE", "SET ROLE uv_r", "GRANT USAGE ON TYPE public.uv_t TO uv_d", "RESET ROLE", "SET ROLE uv_o", "GRANT USAGE ON TYPE public.uv_t TO uv_d", "RESET ROLE")
			ownerOrigins := append(c.baseline("uv_o", "public", "uv_t"), c.origin("uv_o", "uv_r", "YES"), c.origin("uv_o", "uv_d", "NO"))
			all := append(append([][]any(nil), ownerOrigins...), c.origin("uv_r", "uv_d", "NO"))
			held := c.hold(c.conn, uvQuery)
			read := func(want ...[]any) {
				c.check(c.conn, "public", "uv_t", want...)
				c.check(held, "public", "uv_t", want...)
			}
			read(all...)
			c.actor("uv_o", func() { read(ownerOrigins...) })
			c.actor("uv_r", func() { read(c.public(), c.origin("uv_o", "uv_r", "YES"), c.origin("uv_r", "uv_d", "NO")) })
			c.actor("uv_d", func() { read(c.public(), c.origin("uv_o", "uv_d", "NO"), c.origin("uv_r", "uv_d", "NO")) })
			c.actor("uv_m", func() { read(c.public()) })
		})
	})
}
