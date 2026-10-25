package reference_test

import (
	"testing"

	"stuntgres/reference/fixture"
)

func (c *rugCase) familyRow(grantor, grantee, schema, name, family, option string) []any {
	return []any{grantor, grantee, c.database, schema, name, family, "USAGE", option}
}

func (c *rugCase) sequenceACL(name, state string) {
	c.t.Helper()
	q := "SELECT relacl IS NULL AS absent,COALESCE(cardinality(relacl),0)::bigint AS entries FROM pg_class WHERE oid=$1::regclass"
	if state == "nonempty" {
		q = "SELECT relacl IS NULL AS absent,cardinality(relacl)>0 AS nonempty FROM pg_class WHERE oid=$1::regclass"
		c.run(c.conn, fixture.Select(q, []string{"absent", "nonempty"}, []string{"BOOL", "BOOL"}, [][]any{{false, true}}, name))
		return
	}
	c.run(c.conn, fixture.Select(q, []string{"absent", "entries"}, []string{"BOOL", "INT8"}, [][]any{{state == "absent", int64(0)}}, name))
}

func TestReferenceInfoRoleUsageGrantsFamilies(t *testing.T) {
	t.Run("domain", func(t *testing.T) {
		rugScenario(t, func(c *rugCase) {
			c.seed()
			held := c.hold(c.conn, rugQuery)
			c.acl("absent")
			c.actor("rug_o", func() { c.check(held, "public", "rug_t", c.baseline("rug_o", "public", "rug_t")...) })
			c.exec("SET ROLE rug_o", "REVOKE ALL ON DOMAIN public.rug_t FROM PUBLIC,rug_o", "RESET ROLE")
			c.acl("empty")
			c.check(held, "public", "rug_t")
			c.exec("SET ROLE rug_o", "GRANT USAGE ON TYPE public.rug_t TO rug_r", "RESET ROLE")
			c.check(held, "public", "rug_t", c.origin("rug_o", "rug_r", "NO"))
			c.exec("DROP DOMAIN public.rug_t")
			c.check(held, "public", "rug_t")
		})
	})
	t.Run("sequence_raw_and_materialized", func(t *testing.T) {
		rugScenario(t, func(c *rugCase) {
			c.seed()
			c.exec("CREATE SEQUENCE public.rug_s", "ALTER SEQUENCE public.rug_s OWNER TO rug_o")
			held := c.hold(c.conn, rugQuery)
			own := c.familyRow("rug_o", "rug_o", "public", "rug_s", "SEQUENCE", "YES")
			reader := c.familyRow("rug_o", "rug_r", "public", "rug_s", "SEQUENCE", "NO")
			c.sequenceACL("public.rug_s", "absent")
			c.check(held, "public", "rug_s")
			c.actor("rug_o", func() { c.check(held, "public", "rug_s") })
			c.exec("SET ROLE rug_o", "GRANT USAGE ON SEQUENCE public.rug_s TO rug_r", "RESET ROLE")
			c.sequenceACL("public.rug_s", "nonempty")
			c.check(c.conn, "public", "rug_s", own, reader)
			c.check(held, "public", "rug_s", own, reader)
			c.exec("SET ROLE rug_o", "REVOKE USAGE ON SEQUENCE public.rug_s FROM rug_r", "RESET ROLE")
			c.check(held, "public", "rug_s", own)
			c.exec("SET ROLE rug_o", "REVOKE ALL ON SEQUENCE public.rug_s FROM rug_o", "RESET ROLE")
			c.sequenceACL("public.rug_s", "empty")
			c.check(held, "public", "rug_s")
			c.exec("SET ROLE rug_o", "GRANT SELECT,UPDATE ON SEQUENCE public.rug_s TO rug_r", "RESET ROLE")
			c.check(held, "public", "rug_s")
			c.exec("DROP SEQUENCE public.rug_s")
			c.check(held, "public", "rug_s")
		})
	})
	t.Run("sequence_default_privileges", func(t *testing.T) {
		rugScenario(t, func(c *rugCase) {
			c.seed()
			c.exec("GRANT CREATE ON SCHEMA public TO rug_o", "ALTER DEFAULT PRIVILEGES FOR ROLE rug_o GRANT USAGE ON SEQUENCES TO rug_r")
			// Revoke the cluster-level default even when an assertion fails.
			c.t.Cleanup(func() { _, err := c.b.DB.ExecContext(c.p.Ctx, "ALTER DEFAULT PRIVILEGES FOR ROLE rug_o REVOKE USAGE ON SEQUENCES FROM rug_r"); if err != nil { c.t.Error(err) } })
			c.exec("SET ROLE rug_o", "CREATE SEQUENCE public.rug_default", "RESET ROLE")
			c.sequenceACL("public.rug_default", "nonempty")
			c.check(c.conn, "public", "rug_default", c.familyRow("rug_o", "rug_o", "public", "rug_default", "SEQUENCE", "YES"), c.familyRow("rug_o", "rug_r", "public", "rug_default", "SEQUENCE", "NO"))
			c.exec("ALTER DEFAULT PRIVILEGES FOR ROLE rug_o REVOKE USAGE ON SEQUENCES FROM rug_r")
		})
	})
	t.Run("foreign_objects", func(t *testing.T) {
		rugScenario(t, func(c *rugCase) {
			c.seed()
			c.t.Cleanup(func() {
				for _, q := range []string{"DROP SERVER IF EXISTS rug_server CASCADE", "DROP FOREIGN DATA WRAPPER IF EXISTS rug_wrapper CASCADE"} {
					if _, err := c.b.DB.ExecContext(c.p.Ctx, q); err != nil { c.t.Error(err) }
				}
			})
			// Wrapper ownership requires a superuser; keep ordinary reader grants separate.
			c.exec("CREATE FOREIGN DATA WRAPPER rug_wrapper", "ALTER FOREIGN DATA WRAPPER rug_wrapper OWNER TO rug_power", "CREATE SERVER rug_server FOREIGN DATA WRAPPER rug_wrapper", "ALTER SERVER rug_server OWNER TO rug_power")
			held := c.hold(c.conn, rugQuery)
			for _, tc := range []struct{ name, kind, family string }{{"rug_wrapper", "FOREIGN DATA WRAPPER", "FOREIGN DATA WRAPPER"}, {"rug_server", "FOREIGN SERVER", "FOREIGN SERVER"}} {
				own := c.familyRow("rug_power", "rug_power", "", tc.name, tc.family, "YES")
				c.actor("rug_power", func() { c.check(held, "", tc.name, own) })
				c.actor("rug_r", func() { c.check(held, "", tc.name) })
				for _, transition := range []struct{ sql, option string }{{"GRANT USAGE ON "+tc.kind+" "+tc.name+" TO rug_r", "NO"}, {"GRANT USAGE ON "+tc.kind+" "+tc.name+" TO rug_r WITH GRANT OPTION", "YES"}, {"REVOKE GRANT OPTION FOR USAGE ON "+tc.kind+" "+tc.name+" FROM rug_r", "NO"}} {
					c.exec("SET ROLE rug_power", transition.sql, "RESET ROLE")
					reader := c.familyRow("rug_power", "rug_r", "", tc.name, tc.family, transition.option)
					c.check(held, "", tc.name, own, reader)
					c.actor("rug_r", func() { c.check(held, "", tc.name, reader) })
				}
				c.exec("SET ROLE rug_power", "REVOKE ALL ON "+tc.kind+" "+tc.name+" FROM rug_r,rug_power", "RESET ROLE")
				c.check(held, "", tc.name)
			}
			c.exec("DROP SERVER rug_server", "DROP FOREIGN DATA WRAPPER rug_wrapper")
			c.check(held, "", "rug_server")
			c.check(held, "", "rug_wrapper")
		})
	})
	t.Run("collation_and_exclusions", func(t *testing.T) {
		rugScenario(t, func(c *rugCase) {
			c.seed()
			c.exec(`CREATE COLLATION public.rug_c FROM pg_catalog."C"`, "ALTER COLLATION public.rug_c OWNER TO rug_o", "CREATE TYPE public.rug_composite AS(a integer)", "CREATE TYPE public.rug_enum AS ENUM ('a')", "CREATE TYPE public.rug_range AS RANGE(subtype=integer)", "CREATE TABLE public.rug_table(a integer)", "CREATE FUNCTION public.rug_function() RETURNS integer LANGUAGE SQL AS 'SELECT 1'")
			held := c.hold(c.conn, rugQuery)
			c.actor("rug_o", func() { c.check(held, "public", "rug_c", c.familyRow("rug_o", "PUBLIC", "public", "rug_c", "COLLATION", "NO")) })
			c.actor("rug_r", func() { c.check(held, "public", "rug_c") })
			for _, name := range []string{"rug_composite", "_rug_composite", "rug_enum", "_rug_enum", "rug_range", "rug_table", "rug_function", "rug_missing"} { c.check(held, "public", name) }
			c.exec("DROP COLLATION public.rug_c")
			c.check(held, "public", "rug_c")
		})
	})
}
