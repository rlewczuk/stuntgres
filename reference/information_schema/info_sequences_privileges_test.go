package reference_test

import (
	"testing"

	"stuntgres/reference/fixture"
)

func TestReferenceInfoSequencesPrivileges(t *testing.T) {
	isqScenario(t, func(c *isqCase) {
		c.exec("CREATE ROLE isq_actor", "CREATE SCHEMA isq_private", "REVOKE ALL ON SCHEMA isq_private FROM PUBLIC", "CREATE SEQUENCE isq_private.isq_acl", "REVOKE ALL ON SEQUENCE isq_private.isq_acl FROM PUBLIC,isq_actor", "CREATE TABLE isq_private.isq_acl_table(a integer)", "GRANT ALL ON TABLE isq_private.isq_acl_table TO isq_actor", "SET ROLE isq_actor")
		held := c.hold(c.conn, isqQuery)
		c.all(held, "isq_acl")
		denied := fixture.ControlsReferenceDiagnostic("42501", "permission denied for schema isq_private")
		denied.Position = "16"
		control := fixture.ErrorDiagnostic("SELECT nextval('isq_private.isq_acl')", denied)
		control.SelectRows, control.ErrorStage = true, "execute"
		for _, privilege := range []string{"SELECT", "UPDATE", "USAGE"} {
			c.exec("RESET ROLE", "GRANT "+privilege+" ON SEQUENCE isq_private.isq_acl TO isq_actor", "SET ROLE isq_actor")
			c.all(held, "isq_acl", c.def("isq_private", "isq_acl"))
			c.run(c.conn, control)
			// Schema ACL is independently inspected, not inferred from the view.
			if c.b.PG {
				c.run(c.conn, fixture.Select("SELECT has_schema_privilege('isq_private','USAGE') AS allowed", []string{"allowed"}, []string{"BOOL"}, [][]any{{false}}))
			}
			c.exec("RESET ROLE", "REVOKE "+privilege+" ON SEQUENCE isq_private.isq_acl FROM isq_actor", "SET ROLE isq_actor")
			c.all(held, "isq_acl")
		}
		c.exec("RESET ROLE", "GRANT SELECT(last_value) ON TABLE isq_private.isq_acl TO isq_actor", "SET ROLE isq_actor")
		c.all(held, "isq_acl")
	})
}

func TestReferenceInfoSequencesRoles(t *testing.T) {
	isqScenario(t, func(c *isqCase) {
		c.exec("CREATE ROLE isq_owner", "CREATE ROLE isq_actor", "CREATE ROLE isq_grant", "CREATE ROLE isq_middle", "CREATE ROLE isq_set", "CREATE ROLE isq_admin", "CREATE SEQUENCE isq_role", "ALTER SEQUENCE isq_role OWNER TO isq_owner", "REVOKE ALL ON SEQUENCE isq_role FROM isq_owner", "SET ROLE isq_owner")
		held := c.hold(c.conn, isqQuery)
		row := c.def("public", "isq_role")
		c.all(held, "isq_role", row)
		c.exec("RESET ROLE", "GRANT isq_owner TO isq_actor WITH INHERIT TRUE", "SET ROLE isq_actor")
		c.all(held, "isq_role", row)
		c.exec("RESET ROLE", "REVOKE isq_owner FROM isq_actor", "GRANT USAGE ON SEQUENCE isq_role TO isq_grant", "GRANT isq_grant TO isq_middle WITH INHERIT TRUE", "GRANT isq_middle TO isq_actor WITH INHERIT TRUE", "SET ROLE isq_actor")
		c.all(held, "isq_role", row)
		c.exec("RESET ROLE", "GRANT isq_grant TO isq_middle WITH INHERIT FALSE", "SET ROLE isq_actor")
		c.all(held, "isq_role")
		c.exec("RESET ROLE", "GRANT isq_grant TO isq_set WITH INHERIT FALSE, SET TRUE", "GRANT isq_grant TO isq_admin WITH ADMIN TRUE, INHERIT FALSE, SET FALSE", "SET SESSION AUTHORIZATION isq_set")
		c.all(held, "isq_role")
		c.exec("SET ROLE isq_grant")
		c.all(held, "isq_role", row)
		c.exec("RESET ROLE")
		c.all(held, "isq_role")
		c.exec("RESET SESSION AUTHORIZATION", "SET ROLE isq_admin")
		c.all(held, "isq_role")
		c.exec("RESET ROLE", "GRANT USAGE ON SEQUENCE isq_role TO PUBLIC", "SET ROLE isq_actor")
		c.all(held, "isq_role", row)
		c.exec("RESET ROLE", "REVOKE USAGE ON SEQUENCE isq_role FROM PUBLIC", "SET ROLE isq_actor")
		c.all(held, "isq_role")
		for _, role := range []string{"pg_read_all_data", "pg_write_all_data"} {
			c.exec("RESET ROLE", "GRANT "+role+" TO isq_actor", "SET ROLE isq_actor")
			c.all(held, "isq_role", row)
			c.exec("RESET ROLE", "REVOKE "+role+" FROM isq_actor", "SET ROLE isq_actor")
			c.all(held, "isq_role")
		}
	})
}
