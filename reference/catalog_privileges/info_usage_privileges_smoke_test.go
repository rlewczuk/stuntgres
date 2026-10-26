package reference_test

import (
	"testing"

	"stuntgres/reference/fixture"
)

func TestReferenceInfoUsagePrivilegesFeatureSmoke(t *testing.T) {
	upScenario(t, func(c *upCase) {
		c.seed()
		c.exec(`CREATE COLLATION public.up_c FROM pg_catalog."C"`, "ALTER COLLATION public.up_c OWNER TO up_o", "CREATE SEQUENCE public.up_s", "ALTER SEQUENCE public.up_s OWNER TO up_o")
		held := c.hold(c.conn, upQuery)
		peer := c.peer()
		write := func(queries ...string) {
			for _, q := range queries {
				c.run(peer, fixture.Exec(q, 0))
			}
		}
		public := c.origin("up_o", "PUBLIC", "NO")
		collation := c.familyRow("up_o", "PUBLIC", "public", "up_c", "COLLATION", "NO")
		read := func(want ...[]any) {
			c.actor("up_m", func() {
				c.check(c.conn, "public", "up_t", want...)
				c.check(held, "public", "up_t", want...)
				c.check(held, "public", "up_c", collation)
			})
		}
		read(public)
		c.actor("up_m", func() {
			for _, name := range []string{"up_t", "up_c"} {
				s := c.step(rugQuery, nil, "public", name)
				c.run(c.conn, s)
				c.run(c.conn, fixture.Prepared(s))
			}
		})
		c.actor("up_o", func() { c.check(held, "public", "up_s") })
		write("SET ROLE up_o", "GRANT USAGE ON DOMAIN public.up_t TO up_g", "RESET ROLE")
		read(public)
		write("GRANT up_g TO up_m WITH INHERIT TRUE")
		read(public, c.origin("up_o", "up_g", "NO"))
		for _, transition := range []struct{ sql, option string }{
			{"GRANT USAGE ON DOMAIN public.up_t TO up_g WITH GRANT OPTION", "YES"},
			{"REVOKE GRANT OPTION FOR USAGE ON DOMAIN public.up_t FROM up_g", "NO"},
			{"REVOKE USAGE ON DOMAIN public.up_t FROM up_g", ""},
		} {
			write("SET ROLE up_o", transition.sql, "RESET ROLE")
			if transition.option == "" {
				read(public)
			} else {
				read(public, c.origin("up_o", "up_g", transition.option))
			}
		}
		write("SET ROLE up_o", "REVOKE USAGE ON DOMAIN public.up_t FROM PUBLIC", "RESET ROLE")
		read()
		write("SET ROLE up_o", "GRANT USAGE ON DOMAIN public.up_t TO PUBLIC", "GRANT USAGE ON SEQUENCE public.up_s TO up_m", "RESET ROLE")
		read(public)
		reader := c.familyRow("up_o", "up_m", "public", "up_s", "SEQUENCE", "NO")
		owner := c.familyRow("up_o", "up_o", "public", "up_s", "SEQUENCE", "YES")
		c.actor("up_m", func() { c.check(held, "public", "up_s", reader) })
		c.actor("up_o", func() { c.check(held, "public", "up_s", owner, reader) })
		write("SET ROLE up_o", "REVOKE USAGE ON SEQUENCE public.up_s FROM up_m", "RESET ROLE")
		c.actor("up_m", func() { c.check(held, "public", "up_s") })
		c.actor("up_o", func() { c.check(held, "public", "up_s", owner) })
		c.exec("DROP DOMAIN public.up_t", "DROP COLLATION public.up_c", "DROP SEQUENCE public.up_s")
		for _, name := range []string{"up_t", "up_c", "up_s"} {
			c.check(held, "public", name)
		}
	})
}
