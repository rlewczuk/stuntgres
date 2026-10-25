package reference_test

import "testing"

func TestReferenceInfoRoleColumnGrantsPrivileges(t *testing.T) {
	t.Run("four_privileges", func(t *testing.T) {
		rcgScenario(t, func(c *rcgCase) {
			c.seed()
			c.exec("SET ROLE rcg_o", "GRANT SELECT ON rcg_t TO rcg_r", "GRANT INSERT(b),UPDATE(a),REFERENCES(b) ON rcg_t TO rcg_r", "RESET ROLE")
			want := [][]any{c.r("a", "SELECT", "NO"), c.r("b", "SELECT", "NO"), c.r("b", "INSERT", "NO"), c.r("a", "UPDATE", "NO"), c.r("b", "REFERENCES", "NO")}
			c.check(c.conn, "rcg_t", "rcg_r", want...)
			c.exec("SET ROLE rcg_o", "GRANT UPDATE(a),REFERENCES(b) ON rcg_t TO rcg_r WITH GRANT OPTION", "RESET ROLE")
			want[3] = c.r("a", "UPDATE", "YES")
			want[4] = c.r("b", "REFERENCES", "YES")
			c.check(c.conn, "rcg_t", "rcg_r", want...)
			c.exec("SET ROLE rcg_o", "REVOKE ALL PRIVILEGES ON rcg_t FROM rcg_r", "REVOKE INSERT(b),UPDATE(a),REFERENCES(b) ON rcg_t FROM rcg_r", "GRANT ALL PRIVILEGES ON rcg_t TO rcg_r", "RESET ROLE")
			want = nil
			for _, column := range []string{"a", "b"} {
				for _, privilege := range []string{"SELECT", "INSERT", "UPDATE", "REFERENCES"} {
					want = append(want, c.r(column, privilege, "NO"))
				}
			}
			c.check(c.conn, "rcg_t", "rcg_r", want...)
			c.exec("SET ROLE rcg_o", "REVOKE SELECT ON rcg_t FROM rcg_o", "RESET ROLE")
			owner := c.baseline("rcg_o", "rcg_t", "a", "b")
			var remaining [][]any
			for _, row := range owner {
				if row[6] != "SELECT" {
					remaining = append(remaining, row)
				}
			}
			c.check(c.conn, "rcg_t", "rcg_o", remaining...)
		})
	})
	t.Run("raw_union", func(t *testing.T) {
		rcgScenario(t, func(c *rcgCase) {
			c.seed()
			c.exec("SET ROLE rcg_o", "GRANT SELECT ON rcg_t TO rcg_r", "GRANT SELECT(a) ON rcg_t TO rcg_r", "RESET ROLE")
			c.check(c.conn, "rcg_t", "rcg_r", c.r("a", "SELECT", "NO"), c.r("b", "SELECT", "NO"))
			c.exec("SET ROLE rcg_o", "GRANT SELECT(a) ON rcg_t TO rcg_r WITH GRANT OPTION", "RESET ROLE")
			c.check(c.conn, "rcg_t", "rcg_r", c.r("a", "SELECT", "NO"), c.r("a", "SELECT", "YES"), c.r("b", "SELECT", "NO"))
			c.exec("GRANT rcg_o TO rcg_r WITH INHERIT TRUE")
			// Raw false and true survive UNION before ownership turns both into YES.
			c.check(c.conn, "rcg_t", "rcg_r", c.r("a", "SELECT", "YES"), c.r("a", "SELECT", "YES"), c.r("b", "SELECT", "YES"))
		})
	})
}

func TestReferenceInfoRoleColumnGrantsOrigins(t *testing.T) {
	rcgScenario(t, func(c *rcgCase) {
		c.seed()
		c.exec("SET ROLE rcg_o", "GRANT SELECT(a) ON rcg_t TO rcg_d WITH GRANT OPTION", "GRANT SELECT(a) ON rcg_t TO rcg_e WITH GRANT OPTION", "RESET ROLE")
		c.actor("rcg_d", func() { c.exec("GRANT SELECT(a) ON rcg_t TO rcg_r") })
		d := c.row("rcg_o", "rcg_d", "public", "rcg_t", "a", "SELECT", "YES")
		r := c.row("rcg_d", "rcg_r", "public", "rcg_t", "a", "SELECT", "NO")
		c.actor("rcg_r", func() { c.check(c.conn, "rcg_t", "rcg_d"); c.check(c.conn, "rcg_t", "rcg_r", r) })
		c.actor("rcg_d", func() { c.check(c.conn, "rcg_t", "rcg_d", d); c.check(c.conn, "rcg_t", "rcg_r", r) })
		c.actor("rcg_o", func() { c.check(c.conn, "rcg_t", "rcg_d", d); c.check(c.conn, "rcg_t", "rcg_r") })
		c.actor("rcg_p", func() { c.check(c.conn, "rcg_t", "rcg_d"); c.check(c.conn, "rcg_t", "rcg_r") })
		c.actor("rcg_e", func() { c.exec("GRANT SELECT(a) ON rcg_t TO rcg_r") })
		e := c.row("rcg_e", "rcg_r", "public", "rcg_t", "a", "SELECT", "NO")
		c.actor("rcg_r", func() { c.check(c.conn, "rcg_t", "rcg_r", r, e) })
		c.exec("GRANT rcg_d TO rcg_m WITH INHERIT TRUE")
		c.actor("rcg_m", func() { c.check(c.conn, "rcg_t", "rcg_d", d); c.check(c.conn, "rcg_t", "rcg_r", r) })
	})
}
