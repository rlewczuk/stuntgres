package reference_test

import (
	"testing"

	"stuntgres/reference/fixture"
)

func TestReferenceInfoTablePrivilegesPrivileges(t *testing.T) {
	for _, prepared := range []bool{false, true} {
		name := "direct"
		if prepared {
			name = "prepared"
		}
		t.Run(name, func(t *testing.T) {
			tpScenario(t, func(c *tpCase) {
				c.seed()
				c.check(c.conn, "tp_t", "tp_o", c.baseline("tp_o", "tp_t")...)
				c.run(c.conn, fixture.Exec("INSERT INTO tp_t VALUES(7)", 1))
				mutate := func(q string) {
					c.exec("SET ROLE tp_o")
					s := fixture.Exec(q, 0)
					s.Prepared = prepared
					c.run(c.conn, s)
					c.exec("RESET ROLE")
				}
				for _, privilege := range tpPrivileges {
					mutate("GRANT " + privilege + " ON tp_t TO tp_r")
					c.check(c.conn, "tp_t", "tp_r", c.r(privilege, "NO"))
					mutate("GRANT " + privilege + " ON tp_t TO tp_r WITH GRANT OPTION")
					c.check(c.conn, "tp_t", "tp_r", c.r(privilege, "YES"))
					mutate("REVOKE GRANT OPTION FOR " + privilege + " ON tp_t FROM tp_r")
					c.check(c.conn, "tp_t", "tp_r", c.r(privilege, "NO"))
					mutate("REVOKE " + privilege + " ON tp_t FROM tp_r")
					c.check(c.conn, "tp_t", "tp_r")
				}
				mutate("GRANT ALL PRIVILEGES ON tp_t TO tp_r")
				var all [][]any
				for _, privilege := range tpPrivileges {
					all = append(all, c.r(privilege, "NO"))
				}
				c.check(c.conn, "tp_t", "tp_r", all...)
				mutate("REVOKE ALL PRIVILEGES ON tp_t FROM tp_r")
				mutate("GRANT MAINTAIN ON tp_t TO tp_r")
				c.check(c.conn, "tp_t", "tp_r")
				mutate("REVOKE MAINTAIN ON tp_t FROM tp_r")
				mutate("GRANT SELECT(a) ON tp_t TO tp_r")
				c.check(c.conn, "tp_t", "tp_r")
				mutate("REVOKE SELECT(a) ON tp_t FROM tp_r")
				mutate("REVOKE ALL PRIVILEGES ON tp_t FROM tp_o")
				c.check(c.conn, "tp_t", "tp_o")
				c.run(c.conn, fixture.Select("SELECT a FROM tp_t", []string{"a"}, []string{"INT4"}, [][]any{{int64(7)}}))
			})
		})
	}
	t.Run("independent_origins", func(t *testing.T) {
		tpScenario(t, func(c *tpCase) {
			c.seed()
			c.exec("SET ROLE tp_o", "GRANT SELECT ON tp_t TO tp_d,tp_e WITH GRANT OPTION", "RESET ROLE")
			c.actor("tp_d", func() { c.exec("GRANT SELECT ON tp_t TO tp_r") })
			d := c.row("tp_d", "tp_r", "public", "tp_t", "SELECT", "NO")
			c.actor("tp_e", func() { c.exec("GRANT SELECT ON tp_t TO tp_r") })
			e := c.row("tp_e", "tp_r", "public", "tp_t", "SELECT", "NO")
			c.actor("tp_r", func() { c.check(c.conn, "tp_t", "tp_r", d, e); c.check(c.conn, "tp_t", "tp_d") })
			c.actor("tp_d", func() { c.check(c.conn, "tp_t", "tp_r", d) })
			c.actor("tp_e", func() { c.check(c.conn, "tp_t", "tp_r", e) })
			c.actor("tp_o", func() { c.check(c.conn, "tp_t", "tp_r") })
			c.actor("tp_p", func() { c.check(c.conn, "tp_t", "tp_r") })
			c.check(c.conn, "tp_t", "tp_r", d, e)
			c.actor("tp_d", func() { c.exec("REVOKE SELECT ON tp_t FROM tp_r") })
			c.actor("tp_r", func() { c.check(c.conn, "tp_t", "tp_r", e) })
			c.check(c.conn, "tp_t", "tp_r", e)
		})
	})
	t.Run("owner_authority_not_raw_option", func(t *testing.T) {
		tpScenario(t, func(c *tpCase) {
			c.seed()
			c.exec("SET ROLE tp_o", "GRANT SELECT ON tp_t TO tp_r", "RESET ROLE")
			c.check(c.conn, "tp_t", "tp_r", c.r("SELECT", "NO"))
			c.exec("GRANT tp_o TO tp_r WITH INHERIT TRUE")
			c.check(c.conn, "tp_t", "tp_r", c.r("SELECT", "YES"))
			c.exec("REVOKE tp_o FROM tp_r")
			c.check(c.conn, "tp_t", "tp_r", c.r("SELECT", "NO"))
			c.exec("SET ROLE tp_o", "GRANT SELECT ON tp_t TO tp_power", "RESET ROLE")
			c.check(c.conn, "tp_t", "tp_power", c.row("tp_o", "tp_power", "public", "tp_t", "SELECT", "YES"))
		})
	})
	t.Run("stored_supplying_grantor", func(t *testing.T) {
		tpScenario(t, func(c *tpCase) {
			c.seed()
			c.exec("GRANT SELECT ON tp_t TO tp_r")
			c.check(c.conn, "tp_t", "tp_r", c.r("SELECT", "NO"))
			c.exec("SET ROLE tp_o", "REVOKE SELECT ON tp_t FROM tp_r", "GRANT SELECT ON tp_t TO tp_d WITH GRANT OPTION", "RESET ROLE", "GRANT tp_d TO tp_m WITH INHERIT TRUE")
			c.actor("tp_m", func() { c.exec("GRANT SELECT ON tp_t TO tp_r") })
			c.check(c.conn, "tp_t", "tp_r", c.row("tp_d", "tp_r", "public", "tp_t", "SELECT", "NO"))
		})
	})
}
