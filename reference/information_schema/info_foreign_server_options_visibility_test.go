package reference_test

import (
	"stuntgres/reference/fixture"
	"testing"
)

func TestReferenceInfoForeignServerOptionsVisibility(t *testing.T) {
	fsopScenario(t, func(c *fsopCase) {
		c.seed()
		c.exec("CREATE ROLE fsop_owner", "CREATE ROLE fsop_newowner", "CREATE ROLE fsop_group", "CREATE ROLE fsop_reader", "CREATE ROLE fsop_denied", "CREATE ROLE fsop_inherit", "CREATE ROLE fsop_set", "GRANT USAGE ON FOREIGN DATA WRAPPER fsop_wrapper TO fsop_owner,fsop_newowner", "ALTER SERVER fsop_server OWNER TO fsop_owner", "GRANT fsop_owner TO fsop_inherit WITH INHERIT TRUE", "GRANT fsop_owner TO fsop_set WITH INHERIT FALSE, SET TRUE")
		held := c.hold(c.conn, fsopQuery)
		check := func(role string, want [][]any) {
			c.exec("SET SESSION AUTHORIZATION " + role)
			c.check(held, "fsop_server", want)
			c.run(c.conn, fixture.Prepared(c.step(fsopQuery, want, "fsop_server")))
			c.check(c.conn, "fsop_server", want)
			c.exec("RESET SESSION AUTHORIZATION")
		}
		want := c.seedRows()
		c.check(held, "fsop_server", want)
		check("fsop_owner", want)
		check("fsop_inherit", want)
		check("fsop_denied", nil)
		check("fsop_set", nil)
		c.exec("SET SESSION AUTHORIZATION fsop_set", "SET ROLE fsop_owner")
		c.check(held, "fsop_server", want)
		c.exec("RESET ROLE")
		c.check(held, "fsop_server", nil)
		c.exec("RESET SESSION AUTHORIZATION", "GRANT USAGE ON FOREIGN SERVER fsop_server TO fsop_reader")
		check("fsop_reader", want)
		c.exec("REVOKE USAGE ON FOREIGN SERVER fsop_server FROM fsop_reader")
		check("fsop_reader", nil)
		c.exec("GRANT USAGE ON FOREIGN SERVER fsop_server TO fsop_group", "GRANT fsop_group TO fsop_reader WITH INHERIT TRUE")
		check("fsop_reader", want)
		c.exec("REVOKE fsop_group FROM fsop_reader")
		check("fsop_reader", nil)
		c.exec("GRANT USAGE ON FOREIGN SERVER fsop_server TO PUBLIC")
		check("fsop_denied", want)
		c.exec("REVOKE USAGE ON FOREIGN SERVER fsop_server FROM PUBLIC", "REVOKE fsop_owner FROM fsop_inherit")
		check("fsop_denied", nil)
		check("fsop_inherit", nil)
		c.exec("GRANT USAGE ON FOREIGN DATA WRAPPER fsop_wrapper TO fsop_denied")
		check("fsop_denied", nil)
		c.exec("CREATE SERVER fsop_second FOREIGN DATA WRAPPER fsop_wrapper OPTIONS(host 'example')", "ALTER SERVER fsop_second OWNER TO fsop_denied")
		check("fsop_denied", nil)
		c.exec("CREATE SCHEMA fsop_scope", "CREATE FOREIGN TABLE fsop_scope.options(id integer) SERVER fsop_server", "GRANT USAGE ON SCHEMA fsop_scope TO fsop_denied", "GRANT SELECT ON fsop_scope.options TO fsop_denied", "CREATE USER MAPPING FOR fsop_denied SERVER fsop_server OPTIONS(password 'mapping-only')")
		check("fsop_denied", nil)
		c.exec("ALTER SERVER fsop_server OWNER TO fsop_newowner")
		check("fsop_owner", nil)
		check("fsop_newowner", want)
		// Wrapper ownership is independently irrelevant; remove its superuser power
		// before checking so it cannot admit through the separate superuser path.
		c.exec("ALTER ROLE fsop_denied SUPERUSER", "ALTER FOREIGN DATA WRAPPER fsop_wrapper OWNER TO fsop_denied", "ALTER ROLE fsop_denied NOSUPERUSER")
		check("fsop_denied", nil)
	})
}
