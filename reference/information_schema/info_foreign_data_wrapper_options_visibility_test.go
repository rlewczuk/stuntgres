package reference_test

import (
	"testing"

	"stuntgres/reference/fixture"
)

func TestReferenceInfoForeignDataWrapperOptionsVisibility(t *testing.T) {
	fdwoScenario(t, func(c *fdwoCase) {
		c.seed()
		c.exec("CREATE ROLE fdwo_owner SUPERUSER", "CREATE ROLE fdwo_newowner SUPERUSER", "CREATE ROLE fdwo_group", "CREATE ROLE fdwo_reader", "CREATE ROLE fdwo_denied", "CREATE ROLE fdwo_inherit", "CREATE ROLE fdwo_set", "ALTER FOREIGN DATA WRAPPER fdwo_wrapper OWNER TO fdwo_owner", "GRANT fdwo_owner TO fdwo_inherit WITH INHERIT TRUE", "GRANT fdwo_owner TO fdwo_set WITH INHERIT FALSE, SET TRUE")
		held := c.hold(c.conn, fdwoQuery)
		check := func(role string, want [][]any) {
			c.exec("SET SESSION AUTHORIZATION " + role)
			c.check(held, "fdwo_wrapper", want)
			c.run(c.conn, fixture.Prepared(c.step(fdwoQuery, want, "fdwo_wrapper")))
			c.check(c.conn, "fdwo_wrapper", want)
			c.exec("RESET SESSION AUTHORIZATION")
		}
		want := c.seedRows()
		c.check(held, "fdwo_wrapper", want) // Independent superuser, not the owner.
		check("fdwo_owner", want)
		check("fdwo_inherit", want)
		check("fdwo_denied", nil)
		check("fdwo_set", nil)
		c.exec("SET SESSION AUTHORIZATION fdwo_set", "SET ROLE fdwo_owner")
		c.check(held, "fdwo_wrapper", want)
		c.exec("RESET ROLE")
		c.check(held, "fdwo_wrapper", nil)
		c.exec("RESET SESSION AUTHORIZATION", "GRANT USAGE ON FOREIGN DATA WRAPPER fdwo_wrapper TO fdwo_reader")
		check("fdwo_reader", want)
		c.exec("REVOKE USAGE ON FOREIGN DATA WRAPPER fdwo_wrapper FROM fdwo_reader")
		check("fdwo_reader", nil)
		c.exec("GRANT USAGE ON FOREIGN DATA WRAPPER fdwo_wrapper TO fdwo_group", "GRANT fdwo_group TO fdwo_reader WITH INHERIT TRUE")
		check("fdwo_reader", want)
		c.exec("REVOKE fdwo_group FROM fdwo_reader")
		check("fdwo_reader", nil)
		c.exec("GRANT USAGE ON FOREIGN DATA WRAPPER fdwo_wrapper TO PUBLIC")
		check("fdwo_denied", want)
		c.exec("REVOKE USAGE ON FOREIGN DATA WRAPPER fdwo_wrapper FROM PUBLIC", "REVOKE fdwo_owner FROM fdwo_inherit")
		check("fdwo_denied", nil)
		check("fdwo_inherit", nil)
		c.exec("CREATE SERVER fdwo_server FOREIGN DATA WRAPPER fdwo_wrapper", "CREATE SCHEMA fdwo_scope", "CREATE FOREIGN TABLE fdwo_scope.options(id integer) SERVER fdwo_server", "GRANT USAGE ON FOREIGN SERVER fdwo_server TO fdwo_denied", "GRANT USAGE ON SCHEMA fdwo_scope TO fdwo_denied", "GRANT SELECT ON fdwo_scope.options TO fdwo_denied", "CREATE USER MAPPING FOR fdwo_denied SERVER fdwo_server OPTIONS(password 'mapping-only')")
		check("fdwo_denied", nil)
	})
}
