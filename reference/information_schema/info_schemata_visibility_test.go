package reference_test

import (
	"stuntgres/reference/fixture"
	"testing"
)

func TestReferenceInfoSchemataVisibility(t *testing.T) {
	for _, phase := range []string{"acl", "ownership", "negative_proxies", "predefined", "temporary"} {
		t.Run(phase, func(t *testing.T) {
			iscScenario(t, func(c *iscCase) {
				c.exec("CREATE ROLE isc_owner", "CREATE ROLE isc_reader", "CREATE ROLE isc_group", "CREATE ROLE isc_member", "CREATE ROLE isc_set NOINHERIT", "CREATE SCHEMA isc_hidden AUTHORIZATION isc_owner", "REVOKE ALL ON SCHEMA isc_hidden FROM PUBLIC")
				q := iscQuery + " WHERE schema_name='isc_hidden'"
				held := c.hold(q)
				check := func(visible bool) {
					var want [][]any
					if visible {
						want = c.row("isc_hidden", "isc_owner")
					}
					c.check(held, c.projection(q, want))
				}
				admin, err := c.b.DB.Conn(c.p.Ctx)
				if err != nil {
					t.Fatal(err)
				}
				defer admin.Close()
				publish := func(queries ...string) {
					for _, q := range queries {
						c.run(admin, fixture.Exec(q, 0))
					}
				}
				actor := func(role string, visible bool) {
					c.exec("SET SESSION AUTHORIZATION " + role)
					check(visible)
					c.exec("RESET SESSION AUTHORIZATION")
				}
				check(true)
				switch phase {
				case "acl":
					c.exec("SET SESSION AUTHORIZATION isc_reader")
					check(false)
					for _, grantee := range []string{"isc_reader", "PUBLIC"} {
						for _, privilege := range []string{"CREATE", "USAGE", "CREATE, USAGE"} {
							publish("GRANT " + privilege + " ON SCHEMA isc_hidden TO " + grantee)
							check(true)
							publish("REVOKE ALL ON SCHEMA isc_hidden FROM " + grantee)
							check(false)
						}
					}
					publish("GRANT USAGE ON SCHEMA isc_hidden TO isc_reader WITH GRANT OPTION")
					check(true)
					publish("REVOKE ALL ON SCHEMA isc_hidden FROM isc_reader")
					check(false)
					c.exec("RESET SESSION AUTHORIZATION")
					publish("GRANT isc_group TO isc_member WITH INHERIT TRUE, SET TRUE")
					for _, privilege := range []string{"CREATE", "USAGE"} {
						publish("GRANT " + privilege + " ON SCHEMA isc_hidden TO isc_group")
						actor("isc_member", true)
						publish("REVOKE ALL ON SCHEMA isc_hidden FROM isc_group")
						actor("isc_member", false)
					}
				case "ownership":
					publish("REVOKE ALL ON SCHEMA isc_hidden FROM isc_owner", "GRANT isc_owner TO isc_member WITH INHERIT TRUE, SET TRUE", "GRANT isc_owner TO isc_set WITH INHERIT FALSE, SET TRUE")
					actor("isc_owner", true)
					actor("isc_member", true)
					actor("isc_set", false)
					c.exec("SET SESSION AUTHORIZATION isc_set", "SET ROLE isc_owner")
					check(true)
					c.exec("RESET ROLE")
					check(false)
					c.exec("RESET SESSION AUTHORIZATION")
					c.exec("SET SESSION AUTHORIZATION isc_owner")
					check(true)
					publish("ALTER SCHEMA isc_hidden OWNER TO isc_group")
					check(false)
					publish("ALTER SCHEMA isc_hidden OWNER TO isc_owner")
					check(true)
					c.exec("RESET SESSION AUTHORIZATION")
				case "negative_proxies":
					publish("CREATE TABLE isc_hidden.t(v integer)", "CREATE DOMAIN isc_hidden.d AS integer", "CREATE FUNCTION isc_hidden.f() RETURNS integer BEGIN ATOMIC SELECT 1; END", "REVOKE ALL ON DOMAIN isc_hidden.d FROM PUBLIC", "REVOKE ALL ON FUNCTION isc_hidden.f() FROM PUBLIC")
					c.exec("SET SESSION AUTHORIZATION isc_reader")
					check(false)
					for _, grant := range []string{"GRANT SELECT ON isc_hidden.t TO isc_reader", "GRANT USAGE ON DOMAIN isc_hidden.d TO isc_reader", "GRANT EXECUTE ON FUNCTION isc_hidden.f() TO isc_reader"} {
						publish(grant)
						check(false)
					}
					defer publish("REVOKE CREATE ON DATABASE \"" + c.database + "\" FROM isc_reader")
					publish("GRANT CREATE ON DATABASE \"" + c.database + "\" TO isc_reader")
					check(false)
					c.exec("SET search_path=isc_hidden,public")
					check(false)
					c.exec("RESET SESSION AUTHORIZATION")
				case "predefined":
					for _, role := range []string{"pg_read_all_data", "pg_write_all_data"} {
						publish("GRANT " + role + " TO isc_reader WITH INHERIT FALSE, SET TRUE")
						actor("isc_reader", false)
						c.exec("SET SESSION AUTHORIZATION isc_reader", "SET ROLE "+role)
						check(true)
						c.exec("RESET ROLE")
						check(false)
						c.exec("RESET SESSION AUTHORIZATION")
						publish("REVOKE "+role+" FROM isc_reader", "GRANT "+role+" TO isc_reader WITH INHERIT TRUE, SET TRUE")
						actor("isc_reader", true)
						publish("REVOKE " + role + " FROM isc_reader")
					}
				case "temporary":
					c.exec("SET SESSION AUTHORIZATION isc_reader", "CREATE TEMP TABLE isc_temp(v integer)")
					var name string
					if err := c.conn.QueryRowContext(c.p.Ctx, "SELECT nspname FROM pg_catalog.pg_namespace WHERE oid=pg_my_temp_schema()").Scan(&name); err != nil {
						t.Fatal(err)
					}
					var owner string
					if err := c.conn.QueryRowContext(c.p.Ctx, "SELECT pg_get_userbyid(nspowner) FROM pg_catalog.pg_namespace WHERE nspname=$1", name).Scan(&owner); err != nil {
						t.Fatal(err)
					}
					c.routes(c.projection(iscQuery+" WHERE schema_name=$1", c.row(name, owner), name))
					// Another unprivileged session has no ordinary TEMP namespace rights.
					c.run(admin, fixture.Exec("SET SESSION AUTHORIZATION isc_set", 0))
					defer c.run(admin, fixture.Exec("RESET SESSION AUTHORIZATION", 0))
					c.run(admin, c.projection(iscQuery+" WHERE schema_name=$1", nil, name))
					c.exec("RESET SESSION AUTHORIZATION")
				}
			})
		})
	}
}
