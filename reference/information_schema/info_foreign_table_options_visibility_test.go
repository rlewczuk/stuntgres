package reference_test

import (
	"stuntgres/reference/fixture"
	"testing"
)

func (c *ftoCase) actor(held fixture.HeldStatement, role string, want [][]any) {
	c.t.Helper()
	c.exec("SET SESSION AUTHORIZATION " + role)
	c.check(c.conn, "fto_table", want)
	c.run(c.conn, fixture.Prepared(c.step(ftoQuery, want, "fto_table")))
	c.check(held, "fto_table", want)
	c.exec("RESET SESSION AUTHORIZATION")
}

func TestReferenceInfoForeignTableOptionsVisibility(t *testing.T) {
	for _, privilege := range []string{"SELECT", "INSERT", "UPDATE", "DELETE", "TRUNCATE", "REFERENCES", "TRIGGER", "MAINTAIN", "SELECT(a)", "INSERT(a)", "UPDATE(a)", "REFERENCES(a)"} {
		t.Run(privilege, func(t *testing.T) {
			ftoScenario(t, func(c *ftoCase) {
				c.seed()
				c.exec("CREATE ROLE fto_reader", "REVOKE ALL ON SCHEMA fto_scope FROM PUBLIC", "REVOKE ALL ON FOREIGN SERVER fto_server FROM PUBLIC", "GRANT "+privilege+" ON fto_scope.fto_table TO fto_reader")
				held := c.hold(c.conn, ftoQuery)
				want := c.seedRows()
				if privilege == "MAINTAIN" {
					want = nil
				}
				c.actor(held, "fto_reader", want)
				c.exec("REVOKE " + privilege + " ON fto_scope.fto_table FROM fto_reader")
				c.actor(held, "fto_reader", nil)
			})
		})
	}
	t.Run("ownership_and_nonqualifying_rights", func(t *testing.T) {
		ftoScenario(t, func(c *ftoCase) {
			c.seed()
			for _, role := range ftoRoles {
				q := "CREATE ROLE " + role
				if role == "fto_set" {
					q += " NOINHERIT"
				}
				if role == "fto_super" {
					q += " SUPERUSER"
				}
				c.exec(q)
			}
			c.exec("ALTER FOREIGN TABLE fto_scope.fto_table OPTIONS(ADD secret 'full-secret',ADD token 'full-token')", "ALTER FOREIGN TABLE fto_scope.fto_table OWNER TO fto_owner", "REVOKE ALL ON fto_scope.fto_table FROM PUBLIC", "REVOKE ALL ON fto_scope.fto_table FROM fto_owner", "REVOKE ALL ON SCHEMA fto_scope FROM PUBLIC", "REVOKE ALL ON FOREIGN SERVER fto_server FROM PUBLIC", "REVOKE ALL ON FOREIGN DATA WRAPPER fto_wrapper FROM PUBLIC", "GRANT fto_owner TO fto_inherit WITH INHERIT TRUE, SET TRUE", "GRANT fto_owner TO fto_set WITH INHERIT FALSE, SET TRUE")
			full := append(c.seedRows(), c.row("fto_table", "secret", "full-secret"), c.row("fto_table", "token", "full-token"))
			held := c.hold(c.conn, ftoQuery)
			for _, role := range []string{"fto_owner", "fto_inherit", "fto_super"} {
				c.actor(held, role, full)
			}
			c.actor(held, "fto_set", nil)
			c.actor(held, "fto_denied", nil)
			c.exec("SET SESSION AUTHORIZATION fto_set", "SET ROLE fto_owner")
			c.check(held, "fto_table", full)
			c.exec("RESET ROLE")
			c.check(held, "fto_table", nil)
			c.exec("RESET SESSION AUTHORIZATION")
			for _, grant := range []string{"GRANT USAGE ON SCHEMA fto_scope TO fto_reader", "GRANT USAGE ON FOREIGN SERVER fto_server TO fto_reader", "GRANT USAGE ON FOREIGN DATA WRAPPER fto_wrapper TO fto_reader", "CREATE USER MAPPING FOR fto_reader SERVER fto_server OPTIONS(password 'mapping-only')"} {
				c.exec(grant)
				c.actor(held, "fto_reader", nil)
			}
			c.exec("GRANT SELECT(a) ON fto_scope.fto_table TO PUBLIC")
			c.actor(held, "fto_denied", full)
			c.exec("REVOKE SELECT(a) ON fto_scope.fto_table FROM PUBLIC")
			c.actor(held, "fto_denied", nil)
			c.exec("GRANT SELECT(a) ON fto_scope.fto_table TO fto_reader")
			c.actor(held, "fto_reader", full)
			c.exec("ALTER FOREIGN TABLE fto_scope.fto_table DROP COLUMN a")
			c.actor(held, "fto_reader", nil)
		})
	})
}
