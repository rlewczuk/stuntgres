package reference_test

import (
	"stuntgres/reference/fixture"
	"testing"
)

func TestReferenceInfoForeignServersVisibility(t *testing.T) {
	fsvScenario(t, func(t *testing.T, c *fsvCase) {
		c.seed()
		c.exec("CREATE ROLE fsv_owner", "CREATE ROLE fsv_newowner SUPERUSER", "CREATE ROLE fsv_group", "CREATE ROLE fsv_reader", "CREATE ROLE fsv_denied", "CREATE ROLE fsv_inherit", "CREATE ROLE fsv_set", "ALTER FOREIGN DATA WRAPPER fsv_wrapper OWNER TO fsv_newowner", "GRANT USAGE ON FOREIGN DATA WRAPPER fsv_wrapper TO fsv_owner", "ALTER SERVER fsv_text OWNER TO fsv_owner", "GRANT fsv_owner TO fsv_inherit WITH INHERIT TRUE", "GRANT fsv_owner TO fsv_set WITH INHERIT FALSE, SET TRUE")
		held := c.hold(c.conn, fsvQuery)
		check := func(role string, want [][]any) {
			c.exec("SET SESSION AUTHORIZATION " + role)
			c.check(held, "fsv_text", want)
			c.run(c.conn, fixture.Prepared(c.step(fsvQuery, want, "fsv_text")))
			c.check(c.conn, "fsv_text", want)
			c.exec("RESET SESSION AUTHORIZATION")
		}
		want := [][]any{c.row("fsv_text", "fsv_wrapper", "fsv_owner", "é=🙂", "before")}
		c.check(held, "fsv_text", want) // Independent superuser, not server owner.
		check("fsv_owner", want)
		check("fsv_inherit", want)
		check("fsv_denied", nil)
		check("fsv_set", nil)
		c.exec("SET SESSION AUTHORIZATION fsv_set", "SET ROLE fsv_owner")
		c.check(held, "fsv_text", want)
		c.exec("RESET ROLE")
		c.check(held, "fsv_text", nil)
		c.exec("RESET SESSION AUTHORIZATION", "GRANT USAGE ON FOREIGN SERVER fsv_text TO fsv_reader")
		check("fsv_reader", want)
		c.exec("REVOKE USAGE ON FOREIGN SERVER fsv_text FROM fsv_reader")
		check("fsv_reader", nil)
		c.exec("GRANT USAGE ON FOREIGN SERVER fsv_text TO fsv_group", "GRANT fsv_group TO fsv_reader WITH INHERIT TRUE")
		check("fsv_reader", want)
		c.exec("REVOKE fsv_group FROM fsv_reader")
		check("fsv_reader", nil)
		c.exec("GRANT USAGE ON FOREIGN SERVER fsv_text TO PUBLIC")
		check("fsv_denied", want)
		c.exec("REVOKE USAGE ON FOREIGN SERVER fsv_text FROM PUBLIC", "REVOKE fsv_owner FROM fsv_inherit")
		check("fsv_denied", nil)
		check("fsv_inherit", nil)
		c.exec("REVOKE USAGE ON FOREIGN SERVER fsv_text FROM fsv_owner")
		check("fsv_owner", want) // Inherited ownership is independent of ordinary ACL.
		c.exec("GRANT fsv_owner TO fsv_inherit WITH INHERIT TRUE")
		check("fsv_inherit", want)
		c.exec("CREATE SCHEMA fsv_scope", "CREATE FOREIGN TABLE fsv_scope.options(id integer) SERVER fsv_text")
		controls := []struct{ grant, revoke string }{
			{"GRANT USAGE ON FOREIGN DATA WRAPPER fsv_wrapper TO fsv_denied", "REVOKE USAGE ON FOREIGN DATA WRAPPER fsv_wrapper FROM fsv_denied"},
			{"GRANT USAGE ON SCHEMA fsv_scope TO fsv_denied", "REVOKE USAGE ON SCHEMA fsv_scope FROM fsv_denied"},
			{"GRANT SELECT ON fsv_scope.options TO fsv_denied", "REVOKE SELECT ON fsv_scope.options FROM fsv_denied"},
			{"CREATE USER MAPPING FOR fsv_denied SERVER fsv_text OPTIONS(password 'mapping-only')", "DROP USER MAPPING FOR fsv_denied SERVER fsv_text"},
		}
		for _, control := range controls {
			c.exec(control.grant)
			check("fsv_denied", nil)
			c.exec(control.revoke)
			check("fsv_denied", nil)
		}
		for _, control := range controls {
			c.exec(control.grant)
		}
		check("fsv_denied", nil)
		// Make the distinct wrapper owner ordinary: ownership alone cannot admit it.
		c.exec("ALTER ROLE fsv_newowner NOSUPERUSER")
		check("fsv_newowner", nil)
		c.exec("GRANT USAGE ON FOREIGN SERVER fsv_text TO fsv_denied")
		check("fsv_denied", want)
	})
}
