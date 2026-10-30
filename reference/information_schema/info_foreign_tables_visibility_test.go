package reference_test

import (
	"stuntgres/reference/fixture"
	"testing"
)

func (c *iftCase) actor(held fixture.HeldStatement, role string, want [][]any) {
	c.t.Helper()
	c.exec("SET SESSION AUTHORIZATION " + role)
	c.check(c.conn, "ift_table", want)
	c.run(c.conn, fixture.Prepared(c.step(iftQuery, want, "ift_table")))
	c.check(held, "ift_table", want)
	c.exec("RESET SESSION AUTHORIZATION")
}

func TestReferenceInfoForeignTablesVisibility(t *testing.T) {
	for _, privilege := range []string{"SELECT", "INSERT", "UPDATE", "DELETE", "TRUNCATE", "REFERENCES", "TRIGGER", "MAINTAIN", "SELECT(a)", "INSERT(a)", "UPDATE(a)", "REFERENCES(a)"} {
		t.Run(privilege, func(t *testing.T) {
			iftScenario(t, func(c *iftCase) {
				c.seed()
				c.exec("CREATE ROLE ift_reader", "REVOKE ALL ON SCHEMA ift_scope FROM PUBLIC", "REVOKE ALL ON FOREIGN SERVER ift_server FROM PUBLIC", "REVOKE ALL ON FOREIGN DATA WRAPPER ift_wrapper FROM PUBLIC", "GRANT "+privilege+" ON ift_scope.ift_table TO ift_reader")
				held := c.hold(c.conn, iftQuery)
				want := c.seedRows()
				if privilege == "MAINTAIN" {
					want = nil
				}
				c.actor(held, "ift_reader", want)
				if privilege == "SELECT" {
					c.exec("SET SESSION AUTHORIZATION ift_reader")
					c.run(c.conn, fixture.Select("SELECT foreign_server_name FROM information_schema.foreign_servers WHERE foreign_server_name='ift_server'", []string{"foreign_server_name"}, []string{"NAME"}, nil))
					c.exec("RESET SESSION AUTHORIZATION")
				}
				c.exec("REVOKE " + privilege + " ON ift_scope.ift_table FROM ift_reader")
				c.actor(held, "ift_reader", nil)
			})
		})
	}
	t.Run("ownership_and_nonqualifying_rights", func(t *testing.T) {
		iftScenario(t, func(c *iftCase) {
			c.seed()
			for _, role := range iftRoles {
				q := "CREATE ROLE " + role
				if role == "ift_set" {
					q += " NOINHERIT"
				}
				if role == "ift_super" {
					q += " SUPERUSER"
				}
				c.exec(q)
			}
			c.exec("ALTER FOREIGN TABLE ift_scope.ift_table OPTIONS(ADD secret 'full-secret',ADD token 'full-token')", "ALTER FOREIGN TABLE ift_scope.ift_table OWNER TO ift_owner", "REVOKE ALL ON ift_scope.ift_table FROM PUBLIC", "REVOKE ALL ON ift_scope.ift_table FROM ift_owner", "REVOKE ALL ON SCHEMA ift_scope FROM PUBLIC", "REVOKE ALL ON FOREIGN SERVER ift_server FROM PUBLIC", "REVOKE ALL ON FOREIGN DATA WRAPPER ift_wrapper FROM PUBLIC", "GRANT ift_owner TO ift_inherit WITH INHERIT TRUE, SET TRUE", "GRANT ift_owner TO ift_set WITH INHERIT FALSE, SET TRUE")
			full := c.seedRows()
			held := c.hold(c.conn, iftQuery)
			for _, role := range []string{"ift_owner", "ift_inherit", "ift_super"} {
				c.actor(held, role, full)
			}
			c.actor(held, "ift_set", nil)
			c.actor(held, "ift_denied", nil)
			c.exec("SET SESSION AUTHORIZATION ift_set", "SET ROLE ift_owner")
			c.check(held, "ift_table", full)
			c.exec("RESET ROLE")
			c.check(held, "ift_table", nil)
			c.exec("RESET SESSION AUTHORIZATION")
			grants := []struct{ grant, revoke string }{
				{"GRANT USAGE ON SCHEMA ift_scope TO ift_reader", "REVOKE USAGE ON SCHEMA ift_scope FROM ift_reader"},
				{"GRANT USAGE ON FOREIGN SERVER ift_server TO ift_reader", "REVOKE USAGE ON FOREIGN SERVER ift_server FROM ift_reader"},
				{"GRANT USAGE ON FOREIGN DATA WRAPPER ift_wrapper TO ift_reader", "REVOKE USAGE ON FOREIGN DATA WRAPPER ift_wrapper FROM ift_reader"},
				{"CREATE USER MAPPING FOR ift_reader SERVER ift_server OPTIONS(password 'mapping-only')", "DROP USER MAPPING FOR ift_reader SERVER ift_server"},
			}
			for _, right := range grants {
				c.exec(right.grant)
				c.actor(held, "ift_reader", nil)
				c.exec(right.revoke)
				c.actor(held, "ift_reader", nil)
			}
			for _, right := range grants {
				c.exec(right.grant)
			}
			c.actor(held, "ift_reader", nil)
			c.exec("GRANT SELECT(a) ON ift_scope.ift_table TO PUBLIC")
			c.actor(held, "ift_denied", full)
			c.exec("REVOKE SELECT(a) ON ift_scope.ift_table FROM PUBLIC")
			c.actor(held, "ift_denied", nil)
			c.exec("GRANT SELECT(a) ON ift_scope.ift_table TO ift_reader")
			c.actor(held, "ift_reader", full)
			c.exec("GRANT SELECT,UPDATE ON ift_scope.ift_table TO ift_reader")
			c.actor(held, "ift_reader", full)
			c.exec("REVOKE SELECT,UPDATE ON ift_scope.ift_table FROM ift_reader", "ALTER FOREIGN TABLE ift_scope.ift_table DROP COLUMN a")
			c.actor(held, "ift_reader", nil)
		})
	})
}
