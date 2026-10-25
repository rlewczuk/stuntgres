package reference_test

import (
	"testing"

	"stuntgres/reference/fixture"
)

func TestReferenceInfoRoleUsageGrantsFeatureSmoke(t *testing.T) {
	rugScenario(t, func(c *rugCase) {
		c.seed()
		c.exec("SET ROLE rug_o", "REVOKE USAGE ON DOMAIN public.rug_t FROM PUBLIC", "RESET ROLE")
		held := c.hold(c.conn, rugQuery)
		peer := c.peer()
		c.actor("rug_r", func() {
			c.check(held, "public", "rug_t")
			for _, tc := range []struct{ sql, option string }{{"GRANT USAGE ON DOMAIN public.rug_t TO rug_r", "NO"}, {"GRANT USAGE ON DOMAIN public.rug_t TO rug_r WITH GRANT OPTION", "YES"}, {"REVOKE GRANT OPTION FOR USAGE ON DOMAIN public.rug_t FROM rug_r", "NO"}, {"REVOKE USAGE ON DOMAIN public.rug_t FROM rug_r", ""}} {
				for _, q := range []string{"SET ROLE rug_o", tc.sql, "RESET ROLE"} { c.run(peer, fixture.Exec(q, 0)) }
				var want [][]any
				if tc.option != "" { want = [][]any{c.origin("rug_o", "rug_r", tc.option)} }
				c.check(held, "public", "rug_t", want...)
			}
		})
		c.actor("rug_x", func() { c.check(held, "public", "rug_t") })
		c.actor("rug_o", func() { c.check(held, "public", "rug_t", c.origin("rug_o", "rug_o", "YES")) })
		c.exec("CREATE SEQUENCE public.rug_smoke_s", "ALTER SEQUENCE public.rug_smoke_s OWNER TO rug_o")
		c.sequenceACL("public.rug_smoke_s", "absent")
		c.check(held, "public", "rug_smoke_s")
		c.exec("SET ROLE rug_o", "GRANT USAGE ON SEQUENCE public.rug_smoke_s TO rug_r", "RESET ROLE")
		own := c.familyRow("rug_o", "rug_o", "public", "rug_smoke_s", "SEQUENCE", "YES")
		c.check(held, "public", "rug_smoke_s", own, c.familyRow("rug_o", "rug_r", "public", "rug_smoke_s", "SEQUENCE", "NO"))
		c.exec("SET ROLE rug_o", "REVOKE USAGE ON SEQUENCE public.rug_smoke_s FROM rug_r", "RESET ROLE")
		c.check(held, "public", "rug_smoke_s", own)
		c.exec("DROP SEQUENCE public.rug_smoke_s", "DROP DOMAIN public.rug_t")
		c.check(held, "public", "rug_smoke_s")
		c.check(held, "public", "rug_t")
	})
}
