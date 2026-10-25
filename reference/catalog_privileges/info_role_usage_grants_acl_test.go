package reference_test

import (
	"testing"

	"stuntgres/reference/fixture"
)

func TestReferenceInfoRoleUsageGrantsACL(t *testing.T) {
	for _, family := range []string{"DOMAIN", "SEQUENCE"} {
		t.Run(family+"_options", func(t *testing.T) {
			rugScenario(t, func(c *rugCase) {
				c.seed()
				name, target := "rug_t", "DOMAIN public.rug_t"
				if family == "DOMAIN" { c.exec("SET ROLE rug_o", "REVOKE USAGE ON DOMAIN public.rug_t FROM PUBLIC", "RESET ROLE") } else {
					name, target = "rug_s", "SEQUENCE public.rug_s"
					c.exec("CREATE SEQUENCE public.rug_s", "ALTER SEQUENCE public.rug_s OWNER TO rug_o", "SET ROLE rug_o", "GRANT USAGE ON SEQUENCE public.rug_s TO rug_o", "RESET ROLE")
				}
				held := c.hold(c.conn, rugQuery)
				own := c.familyRow("rug_o", "rug_o", "public", name, family, "YES")
				read := func(option string) {
					want := [][]any{own}
					if option != "" { want = append(want, c.familyRow("rug_o", "rug_r", "public", name, family, option)) }
					c.check(c.conn, "public", name, want...)
					c.check(held, "public", name, want...)
				}
				read("")
				peer := c.peer()
				for _, tc := range []struct{ sql, option string }{{"GRANT USAGE ON "+target+" TO rug_r", "NO"}, {"GRANT USAGE ON "+target+" TO rug_r WITH GRANT OPTION", "YES"}, {"REVOKE GRANT OPTION FOR USAGE ON "+target+" FROM rug_r", "NO"}, {"REVOKE USAGE ON "+target+" FROM rug_r", ""}} {
					for _, q := range []string{"SET ROLE rug_o", tc.sql, "RESET ROLE"} { c.run(peer, fixture.Exec(q, 0)) }
					read(tc.option)
				}
			})
		})
	}
	t.Run("independent_grantors", func(t *testing.T) {
		rugScenario(t, func(c *rugCase) {
			c.seed()
			c.exec("SET ROLE rug_o", "REVOKE USAGE ON DOMAIN public.rug_t FROM PUBLIC", "GRANT USAGE ON DOMAIN public.rug_t TO rug_d WITH GRANT OPTION", "GRANT USAGE ON DOMAIN public.rug_t TO rug_r", "RESET ROLE", "SET ROLE rug_d", "GRANT USAGE ON DOMAIN public.rug_t TO rug_r", "RESET ROLE")
			held := c.hold(c.conn, rugQuery)
			own, delegate, direct, indirect := c.origin("rug_o", "rug_o", "YES"), c.origin("rug_o", "rug_d", "YES"), c.origin("rug_o", "rug_r", "NO"), c.origin("rug_d", "rug_r", "NO")
			c.check(held, "public", "rug_t", own, delegate, direct, indirect)
			c.actor("rug_o", func() { c.check(held, "public", "rug_t", own, delegate, direct) })
			c.actor("rug_r", func() { c.check(held, "public", "rug_t", direct, indirect) })
			c.exec("SET ROLE rug_o", "REVOKE USAGE ON DOMAIN public.rug_t FROM rug_r", "RESET ROLE")
			c.check(held, "public", "rug_t", own, delegate, indirect)
			c.actor("rug_r", func() { c.check(held, "public", "rug_t", indirect) })
			c.exec("SET ROLE rug_o")
			c.run(c.conn, fixture.ImplicitError("REVOKE GRANT OPTION FOR USAGE ON DOMAIN public.rug_t FROM rug_d RESTRICT", fixture.Diagnostic{Code: "2BP01", Message: "dependent privileges exist", Hint: "Use CASCADE to revoke them too."}))
			c.exec("RESET ROLE")
			c.check(held, "public", "rug_t", own, delegate, indirect)
			c.exec("SET ROLE rug_o", "GRANT USAGE ON DOMAIN public.rug_t TO rug_r", "REVOKE GRANT OPTION FOR USAGE ON DOMAIN public.rug_t FROM rug_d CASCADE", "RESET ROLE")
			// CASCADE removes the dependent origin, not the independent owner grant.
			c.check(held, "public", "rug_t", own, c.origin("rug_o", "rug_d", "NO"), direct)
			c.actor("rug_r", func() { c.check(held, "public", "rug_t", direct) })
		})
	})
}
