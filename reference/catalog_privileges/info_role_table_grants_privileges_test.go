package reference_test

import (
	"testing"

	"stuntgres/reference/fixture"
)

func TestReferenceInfoRoleTableGrantsPrivileges(t *testing.T) {
	for _, prepared := range []bool{false, true} {
		name := "direct"
		if prepared {
			name = "prepared"
		}
		t.Run(name, func(t *testing.T) {
			rtgScenario(t, func(c *rtgCase) {
				c.seed()
				c.check(c.conn, "rtg_t", "rtg_o", c.baseline("rtg_o", "rtg_t")...)
				c.run(c.conn, fixture.Exec("INSERT INTO rtg_t VALUES(7)", 1))
				mutate := func(q string) {
					c.exec("SET ROLE rtg_o")
					s := fixture.Exec(q, 0)
					s.Prepared = prepared
					c.run(c.conn, s)
					c.exec("RESET ROLE")
				}
				for _, privilege := range rtgPrivileges {
					mutate("GRANT " + privilege + " ON rtg_t TO rtg_r")
					c.check(c.conn, "rtg_t", "rtg_r", c.r(privilege, "NO"))
					mutate("GRANT " + privilege + " ON rtg_t TO rtg_r WITH GRANT OPTION")
					c.check(c.conn, "rtg_t", "rtg_r", c.r(privilege, "YES"))
					mutate("REVOKE GRANT OPTION FOR " + privilege + " ON rtg_t FROM rtg_r")
					c.check(c.conn, "rtg_t", "rtg_r", c.r(privilege, "NO"))
					mutate("REVOKE " + privilege + " ON rtg_t FROM rtg_r")
					c.check(c.conn, "rtg_t", "rtg_r")
				}
				mutate("GRANT ALL PRIVILEGES ON rtg_t TO rtg_r")
				var all [][]any
				for _, privilege := range rtgPrivileges {
					all = append(all, c.r(privilege, "NO"))
				}
				c.check(c.conn, "rtg_t", "rtg_r", all...)
				mutate("REVOKE ALL PRIVILEGES ON rtg_t FROM rtg_r")
				mutate("GRANT MAINTAIN ON rtg_t TO rtg_r")
				c.check(c.conn, "rtg_t", "rtg_r")
				mutate("REVOKE MAINTAIN ON rtg_t FROM rtg_r")
				mutate("GRANT SELECT(a) ON rtg_t TO rtg_r")
				c.check(c.conn, "rtg_t", "rtg_r")
				mutate("REVOKE SELECT(a) ON rtg_t FROM rtg_r")
				mutate("REVOKE ALL PRIVILEGES ON rtg_t FROM rtg_o")
				c.check(c.conn, "rtg_t", "rtg_o")
				c.run(c.conn, fixture.Select("SELECT a FROM rtg_t", []string{"a"}, []string{"INT4"}, [][]any{{int64(7)}}))
			})
		})
	}
	t.Run("independent_origins", func(t *testing.T) {
		rtgScenario(t, func(c *rtgCase) {
			c.seed()
			c.exec("SET ROLE rtg_o", "GRANT SELECT ON rtg_t TO rtg_d,rtg_e WITH GRANT OPTION", "RESET ROLE")
			c.actor("rtg_d", func() { c.exec("GRANT SELECT ON rtg_t TO rtg_r") })
			d := c.row("rtg_d", "rtg_r", "public", "rtg_t", "SELECT", "NO")
			c.actor("rtg_e", func() { c.exec("GRANT SELECT ON rtg_t TO rtg_r") })
			e := c.row("rtg_e", "rtg_r", "public", "rtg_t", "SELECT", "NO")
			c.actor("rtg_r", func() { c.check(c.conn, "rtg_t", "rtg_r", d, e); c.check(c.conn, "rtg_t", "rtg_d") })
			c.actor("rtg_d", func() { c.check(c.conn, "rtg_t", "rtg_r", d) })
			c.actor("rtg_e", func() { c.check(c.conn, "rtg_t", "rtg_r", e) })
			c.actor("rtg_o", func() { c.check(c.conn, "rtg_t", "rtg_r") })
			c.actor("rtg_p", func() { c.check(c.conn, "rtg_t", "rtg_r") })
			c.check(c.conn, "rtg_t", "rtg_r", d, e)
		})
	})
	t.Run("owner_authority_not_raw_option", func(t *testing.T) {
		rtgScenario(t, func(c *rtgCase) {
			c.seed()
			c.exec("SET ROLE rtg_o", "GRANT SELECT ON rtg_t TO rtg_r", "RESET ROLE")
			c.check(c.conn, "rtg_t", "rtg_r", c.r("SELECT", "NO"))
			c.exec("GRANT rtg_o TO rtg_r WITH INHERIT TRUE")
			c.check(c.conn, "rtg_t", "rtg_r", c.r("SELECT", "YES"))
			c.exec("REVOKE rtg_o FROM rtg_r")
			c.check(c.conn, "rtg_t", "rtg_r", c.r("SELECT", "NO"))
			c.exec("SET ROLE rtg_o", "GRANT SELECT ON rtg_t TO rtg_power", "RESET ROLE")
			c.check(c.conn, "rtg_t", "rtg_power", c.row("rtg_o", "rtg_power", "public", "rtg_t", "SELECT", "YES"))
		})
	})
}
