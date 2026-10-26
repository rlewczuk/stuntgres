package reference_test

import (
	"testing"

	"stuntgres/reference/fixture"
)

func TestReferenceInfoColumnOptionsVisibility(t *testing.T) {
	copScenario(t, func(c *copCase) {
		c.seed()
		for _, role := range copRoles {
			q := "CREATE ROLE " + role
			if role == "cop_set" {
				q += " NOINHERIT"
			}
			c.exec(q)
		}
		c.exec("REVOKE ALL ON SCHEMA cop_scope FROM PUBLIC", "REVOKE ALL ON FOREIGN SERVER cop_server FROM PUBLIC", "ALTER FOREIGN TABLE cop_scope.options OWNER TO cop_owner", "REVOKE ALL ON cop_scope.options FROM PUBLIC", "REVOKE ALL ON cop_scope.options FROM cop_owner", "GRANT cop_owner TO cop_inherit WITH INHERIT TRUE, SET TRUE", "GRANT cop_owner TO cop_set WITH INHERIT FALSE, SET TRUE")
		full := c.seedRows()
		first := full[:2]
		held := c.hold(c.conn, copQuery)
		actor := func(role string, want [][]any) {
			c.exec("SET SESSION AUTHORIZATION " + role)
			c.check(c.conn, "cop_scope", want)
			c.run(c.conn, fixture.Prepared(c.step(copQuery, want, "cop_scope")))
			c.check(held, "cop_scope", want)
			c.exec("RESET SESSION AUTHORIZATION")
		}
		c.check(held, "cop_scope", full)
		actor("cop_owner", full)
		actor("cop_inherit", full)
		actor("cop_set", nil)
		actor("cop_denied", nil)
		c.exec("SET SESSION AUTHORIZATION cop_set")
		c.check(held, "cop_scope", nil)
		c.exec("SET ROLE cop_owner")
		c.check(held, "cop_scope", full)
		c.exec("RESET ROLE")
		c.check(held, "cop_scope", nil)
		c.exec("RESET SESSION AUTHORIZATION")
		for _, priv := range []string{"SELECT", "INSERT", "UPDATE", "REFERENCES"} {
			c.exec("GRANT " + priv + "(id) ON cop_scope.options TO cop_reader")
			actor("cop_reader", first)
			c.exec("REVOKE " + priv + "(id) ON cop_scope.options FROM cop_reader")
			actor("cop_reader", nil)
			c.exec("GRANT " + priv + " ON cop_scope.options TO cop_reader")
			actor("cop_reader", full)
			c.exec("REVOKE " + priv + " ON cop_scope.options FROM cop_reader")
		}
		for _, priv := range []string{"DELETE", "TRUNCATE", "TRIGGER", "MAINTAIN"} {
			c.exec("GRANT " + priv + " ON cop_scope.options TO cop_reader")
			actor("cop_reader", nil)
			c.exec("REVOKE " + priv + " ON cop_scope.options FROM cop_reader")
		}
		c.exec("GRANT USAGE ON SCHEMA cop_scope TO cop_reader")
		actor("cop_reader", nil)
		c.exec("REVOKE USAGE ON SCHEMA cop_scope FROM cop_reader", "GRANT USAGE ON FOREIGN SERVER cop_server TO cop_reader")
		actor("cop_reader", nil)
		c.exec("REVOKE USAGE ON FOREIGN SERVER cop_server FROM cop_reader", "CREATE USER MAPPING FOR cop_reader SERVER cop_server OPTIONS(password 'mapping-only')")
		actor("cop_reader", nil)
		c.exec("GRANT SELECT(id) ON cop_scope.options TO PUBLIC")
		actor("cop_denied", first)
		c.exec("GRANT UPDATE(id) ON cop_scope.options TO cop_reader")
		actor("cop_reader", first)
		c.exec("REVOKE SELECT(id) ON cop_scope.options FROM PUBLIC")
		actor("cop_denied", nil)
		c.exec("REVOKE UPDATE(id) ON cop_scope.options FROM cop_reader", "GRANT SELECT(id) ON cop_scope.options TO cop_group", "GRANT cop_group TO cop_reader WITH INHERIT TRUE, SET TRUE")
		actor("cop_reader", first)
		c.exec("REVOKE cop_group FROM cop_reader")
		actor("cop_reader", nil)
		c.exec("GRANT cop_owner TO cop_reader WITH INHERIT TRUE, SET TRUE")
		actor("cop_reader", full)
		c.exec("REVOKE cop_owner FROM cop_reader")
		actor("cop_reader", nil)
		c.exec("ALTER FOREIGN TABLE cop_scope.options OWNER TO cop_newowner")
		actor("cop_owner", nil)
		actor("cop_newowner", full)
	})
}
