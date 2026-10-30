package reference_test

import (
	"testing"

	"stuntgres/reference/fixture"
)

func TestReferenceInfoForeignDataWrappersVisibility(t *testing.T) {
	fdwvScenario(t, func(t *testing.T, c *fdwvCase) {
		c.seed()
		c.exec("CREATE ROLE fdwview_owner SUPERUSER", "CREATE ROLE fdwview_newowner SUPERUSER", "CREATE ROLE fdwview_group", "CREATE ROLE fdwview_reader", "CREATE ROLE fdwview_denied", "CREATE ROLE fdwview_inherit", "CREATE ROLE fdwview_set", "ALTER FOREIGN DATA WRAPPER fdwview_wrapper OWNER TO fdwview_owner", "GRANT fdwview_owner TO fdwview_inherit WITH INHERIT TRUE", "GRANT fdwview_owner TO fdwview_set WITH INHERIT FALSE, SET TRUE")
		held := c.hold(c.conn, fdwvQuery)
		check := func(role string, want [][]any) {
			c.exec("SET SESSION AUTHORIZATION " + role)
			c.check(held, "fdwview_wrapper", want)
			c.run(c.conn, fixture.Prepared(c.step(fdwvQuery, want, "fdwview_wrapper")))
			c.check(c.conn, "fdwview_wrapper", want)
			c.exec("RESET SESSION AUTHORIZATION")
		}
		want := [][]any{c.row("fdwview_wrapper", "fdwview_owner")}
		c.check(held, "fdwview_wrapper", want) // Independent superuser, not the owner.
		check("fdwview_owner", want)
		check("fdwview_inherit", want)
		check("fdwview_denied", nil)
		check("fdwview_set", nil)
		c.exec("SET SESSION AUTHORIZATION fdwview_set", "SET ROLE fdwview_owner")
		c.check(held, "fdwview_wrapper", want)
		c.exec("RESET ROLE")
		c.check(held, "fdwview_wrapper", nil)
		c.exec("RESET SESSION AUTHORIZATION", "GRANT USAGE ON FOREIGN DATA WRAPPER fdwview_wrapper TO fdwview_reader")
		check("fdwview_reader", want)
		c.exec("REVOKE USAGE ON FOREIGN DATA WRAPPER fdwview_wrapper FROM fdwview_reader")
		check("fdwview_reader", nil)
		c.exec("GRANT USAGE ON FOREIGN DATA WRAPPER fdwview_wrapper TO fdwview_group", "GRANT fdwview_group TO fdwview_reader WITH INHERIT TRUE")
		check("fdwview_reader", want)
		c.exec("REVOKE fdwview_group FROM fdwview_reader")
		check("fdwview_reader", nil)
		c.exec("GRANT USAGE ON FOREIGN DATA WRAPPER fdwview_wrapper TO PUBLIC")
		check("fdwview_denied", want)
		c.exec("REVOKE USAGE ON FOREIGN DATA WRAPPER fdwview_wrapper FROM PUBLIC", "REVOKE fdwview_owner FROM fdwview_inherit")
		check("fdwview_denied", nil)
		check("fdwview_inherit", nil)
		c.exec("CREATE SERVER fdwview_server FOREIGN DATA WRAPPER fdwview_wrapper", "CREATE SCHEMA fdwview_scope", "CREATE FOREIGN TABLE fdwview_scope.options(id integer) SERVER fdwview_server")
		controls := []struct{ grant, revoke string }{
			{"GRANT USAGE ON FOREIGN SERVER fdwview_server TO fdwview_denied", "REVOKE USAGE ON FOREIGN SERVER fdwview_server FROM fdwview_denied"},
			{"GRANT USAGE ON SCHEMA fdwview_scope TO fdwview_denied", "REVOKE USAGE ON SCHEMA fdwview_scope FROM fdwview_denied"},
			{"GRANT SELECT ON fdwview_scope.options TO fdwview_denied", "REVOKE SELECT ON fdwview_scope.options FROM fdwview_denied"},
			{"CREATE USER MAPPING FOR fdwview_denied SERVER fdwview_server OPTIONS(password 'mapping-only')", "DROP USER MAPPING FOR fdwview_denied SERVER fdwview_server"},
		}
		for _, control := range controls {
			c.exec(control.grant)
			check("fdwview_denied", nil)
			c.exec(control.revoke)
			check("fdwview_denied", nil)
		}
		for _, control := range controls {
			c.exec(control.grant)
		}
		check("fdwview_denied", nil)
	})
}
