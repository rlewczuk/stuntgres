package reference_test

import "testing"

func TestReferenceInfoRoleUdtGrantsACLTransitions(t *testing.T) {
	t.Run("options_and_empty", func(t *testing.T) {
		ugScenario(t, func(c *ugCase) {
			c.seed()
			held := c.hold(c.conn, ugQuery)
			read := func(want ...[]any) {
				c.check(c.conn, "public", "ug_t", want...)
				c.check(held, "public", "ug_t", want...)
			}
			c.acl("absent")
			read(c.baseline("ug_o", "public", "ug_t")...)
			c.exec("SET ROLE ug_o", "REVOKE USAGE ON TYPE public.ug_t FROM PUBLIC", "RESET ROLE")
			own := c.origin("ug_o", "ug_o", "YES")
			c.acl("nonempty")
			read(own)
			for _, tc := range []struct{ sql, option string }{
				{"GRANT USAGE ON TYPE public.ug_t TO ug_r", "NO"},
				{"GRANT USAGE ON TYPE public.ug_t TO ug_r WITH GRANT OPTION", "YES"},
				{"REVOKE GRANT OPTION FOR USAGE ON TYPE public.ug_t FROM ug_r", "NO"},
			} {
				c.exec("SET ROLE ug_o", tc.sql, "RESET ROLE")
				read(own, c.origin("ug_o", "ug_r", tc.option))
			}
			c.exec("SET ROLE ug_o", "REVOKE USAGE ON TYPE public.ug_t FROM ug_r", "RESET ROLE")
			read(own)
			c.exec("SET ROLE ug_o", "REVOKE USAGE ON TYPE public.ug_t FROM ug_o", "RESET ROLE")
			c.acl("empty")
			read()
			c.actor("ug_o", func() { read() })
		})
	})
	t.Run("grantor_origins", func(t *testing.T) {
		ugScenario(t, func(c *ugCase) {
			c.seed()
			c.exec("SET ROLE ug_o", "GRANT USAGE ON TYPE public.ug_t TO ug_r WITH GRANT OPTION", "RESET ROLE", "SET ROLE ug_r", "GRANT USAGE ON TYPE public.ug_t TO ug_d", "RESET ROLE", "SET ROLE ug_o", "GRANT USAGE ON TYPE public.ug_t TO ug_d", "RESET ROLE")
			baseline := c.baseline("ug_o", "public", "ug_t")
			ownerOrigins := append(baseline, c.origin("ug_o", "ug_r", "YES"), c.origin("ug_o", "ug_d", "NO"))
			all := append(append([][]any(nil), ownerOrigins...), c.origin("ug_r", "ug_d", "NO"))
			held := c.hold(c.conn, ugQuery)
			c.check(held, "public", "ug_t", all...)
			c.actor("ug_o", func() {
				c.check(c.conn, "public", "ug_t", ownerOrigins...)
				c.check(held, "public", "ug_t", ownerOrigins...)
			})
			c.actor("ug_r", func() {
				c.check(held, "public", "ug_t", c.origin("ug_o", "ug_r", "YES"), c.origin("ug_r", "ug_d", "NO"))
			})
			c.actor("ug_d", func() {
				c.check(held, "public", "ug_t", c.origin("ug_o", "ug_d", "NO"), c.origin("ug_r", "ug_d", "NO"))
			})
			c.actor("ug_p", func() { c.check(held, "public", "ug_t") })
		})
	})
}
