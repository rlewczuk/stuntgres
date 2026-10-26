package reference_test

import (
	"testing"

	"stuntgres/reference/fixture"
)

func TestReferenceInfoUsagePrivilegesACLRefresh(t *testing.T) {
	for _, family := range []string{"DOMAIN", "SEQUENCE"} {
		t.Run(family+"_options", func(t *testing.T) {
			upScenario(t, func(c *upCase) {
				c.seed()
				name, target := "up_t", "DOMAIN public.up_t"
				if family == "DOMAIN" {
					c.exec("SET ROLE up_o", "REVOKE USAGE ON DOMAIN public.up_t FROM PUBLIC", "RESET ROLE")
				} else {
					name, target = "up_s", "SEQUENCE public.up_s"
					c.exec("CREATE SEQUENCE public.up_s", "ALTER SEQUENCE public.up_s OWNER TO up_o", "SET ROLE up_o", "GRANT USAGE ON SEQUENCE public.up_s TO up_o", "RESET ROLE")
				}
				held := c.hold(c.conn, upQuery)
				own := c.familyRow("up_o", "up_o", "public", name, family, "YES")
				read := func(option string) {
					want := [][]any{own}
					if option != "" {
						want = append(want, c.familyRow("up_o", "up_r", "public", name, family, option))
					}
					c.check(c.conn, "public", name, want...)
					c.check(held, "public", name, want...)
				}
				read("")
				peer := c.peer()
				for _, tc := range []struct{ sql, option string }{{"GRANT USAGE ON " + target + " TO up_r", "NO"}, {"GRANT USAGE ON " + target + " TO up_r WITH GRANT OPTION", "YES"}, {"REVOKE GRANT OPTION FOR USAGE ON " + target + " FROM up_r", "NO"}, {"REVOKE USAGE ON " + target + " FROM up_r", ""}} {
					for _, q := range []string{"SET ROLE up_o", tc.sql, "RESET ROLE"} {
						c.run(peer, fixture.Exec(q, 0))
					}
					read(tc.option)
				}
			})
		})
	}
	t.Run("independent_grantors", func(t *testing.T) {
		upScenario(t, func(c *upCase) {
			c.seed()
			c.exec("SET ROLE up_o", "REVOKE USAGE ON DOMAIN public.up_t FROM PUBLIC", "GRANT USAGE ON DOMAIN public.up_t TO up_d WITH GRANT OPTION", "GRANT USAGE ON DOMAIN public.up_t TO up_r", "RESET ROLE", "SET ROLE up_d", "GRANT USAGE ON DOMAIN public.up_t TO up_r", "RESET ROLE")
			held := c.hold(c.conn, upQuery)
			own, delegate, direct, indirect := c.origin("up_o", "up_o", "YES"), c.origin("up_o", "up_d", "YES"), c.origin("up_o", "up_r", "NO"), c.origin("up_d", "up_r", "NO")
			c.check(held, "public", "up_t", own, delegate, direct, indirect)
			c.actor("up_o", func() { c.check(held, "public", "up_t", own, delegate, direct) })
			c.actor("up_r", func() { c.check(held, "public", "up_t", direct, indirect) })
			c.exec("SET ROLE up_o", "REVOKE USAGE ON DOMAIN public.up_t FROM up_r", "RESET ROLE")
			c.check(held, "public", "up_t", own, delegate, indirect)
			c.actor("up_r", func() { c.check(held, "public", "up_t", indirect) })
			c.exec("SET ROLE up_o")
			c.run(c.conn, fixture.ImplicitError("REVOKE GRANT OPTION FOR USAGE ON DOMAIN public.up_t FROM up_d RESTRICT", fixture.Diagnostic{Code: "2BP01", Message: "dependent privileges exist", Hint: "Use CASCADE to revoke them too."}))
			c.exec("RESET ROLE")
			c.check(held, "public", "up_t", own, delegate, indirect)
			c.exec("SET ROLE up_o", "GRANT USAGE ON DOMAIN public.up_t TO up_r", "REVOKE GRANT OPTION FOR USAGE ON DOMAIN public.up_t FROM up_d CASCADE", "RESET ROLE")
			// CASCADE removes the dependent origin, not the independent owner grant.
			c.check(held, "public", "up_t", own, c.origin("up_o", "up_d", "NO"), direct)
			c.actor("up_r", func() { c.check(held, "public", "up_t", direct) })
		})
	})
}
