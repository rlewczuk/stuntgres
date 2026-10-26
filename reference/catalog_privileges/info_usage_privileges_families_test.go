package reference_test

import (
	"testing"

	"stuntgres/reference/fixture"
)

func (c *upCase) familyRow(grantor, grantee, schema, name, family, option string) []any {
	return []any{grantor, grantee, c.database, schema, name, family, "USAGE", option}
}

func (c *upCase) sequenceACL(name, state string) {
	c.t.Helper()
	q := "SELECT relacl IS NULL AS absent,COALESCE(cardinality(relacl),0)::bigint AS entries FROM pg_class WHERE oid=$1::regclass"
	if state == "nonempty" {
		q = "SELECT relacl IS NULL AS absent,cardinality(relacl)>0 AS nonempty FROM pg_class WHERE oid=$1::regclass"
		c.run(c.conn, fixture.Select(q, []string{"absent", "nonempty"}, []string{"BOOL", "BOOL"}, [][]any{{false, true}}, name))
		return
	}
	c.run(c.conn, fixture.Select(q, []string{"absent", "entries"}, []string{"BOOL", "INT8"}, [][]any{{state == "absent", int64(0)}}, name))
}

func TestReferenceInfoUsagePrivilegesFamilies(t *testing.T) {
	t.Run("domain", func(t *testing.T) {
		upScenario(t, func(c *upCase) {
			c.seed()
			held := c.hold(c.conn, upQuery)
			c.acl("absent")
			c.actor("up_o", func() { c.check(held, "public", "up_t", c.baseline("up_o", "public", "up_t")...) })
			c.actor("up_r", func() { c.check(held, "public", "up_t", c.origin("up_o", "PUBLIC", "NO")) })
			c.exec("SET ROLE up_o", "REVOKE ALL ON DOMAIN public.up_t FROM PUBLIC,up_o", "RESET ROLE")
			c.acl("empty")
			c.check(held, "public", "up_t")
			c.exec("SET ROLE up_o", "GRANT USAGE ON TYPE public.up_t TO up_r", "RESET ROLE")
			c.check(held, "public", "up_t", c.origin("up_o", "up_r", "NO"))
			c.exec("DROP DOMAIN public.up_t")
			c.check(held, "public", "up_t")
		})
	})
	t.Run("sequence_raw_and_materialized", func(t *testing.T) {
		upScenario(t, func(c *upCase) {
			c.seed()
			c.exec("CREATE SEQUENCE public.up_s", "ALTER SEQUENCE public.up_s OWNER TO up_o")
			held := c.hold(c.conn, upQuery)
			own := c.familyRow("up_o", "up_o", "public", "up_s", "SEQUENCE", "YES")
			reader := c.familyRow("up_o", "up_r", "public", "up_s", "SEQUENCE", "NO")
			c.sequenceACL("public.up_s", "absent")
			c.check(held, "public", "up_s")
			c.actor("up_o", func() { c.check(held, "public", "up_s") })
			c.exec("SET ROLE up_o", "GRANT USAGE ON SEQUENCE public.up_s TO up_r", "RESET ROLE")
			c.sequenceACL("public.up_s", "nonempty")
			c.check(c.conn, "public", "up_s", own, reader)
			c.check(held, "public", "up_s", own, reader)
			c.exec("SET ROLE up_o", "REVOKE USAGE ON SEQUENCE public.up_s FROM up_r", "RESET ROLE")
			c.check(held, "public", "up_s", own)
			c.exec("SET ROLE up_o", "GRANT USAGE ON SEQUENCE public.up_s TO PUBLIC", "RESET ROLE")
			c.actor("up_p", func() {
				c.check(held, "public", "up_s", c.familyRow("up_o", "PUBLIC", "public", "up_s", "SEQUENCE", "NO"))
			})
			c.exec("SET ROLE up_o", "REVOKE USAGE ON SEQUENCE public.up_s FROM PUBLIC", "RESET ROLE")
			c.exec("SET ROLE up_o", "REVOKE ALL ON SEQUENCE public.up_s FROM up_o", "RESET ROLE")
			c.sequenceACL("public.up_s", "empty")
			c.check(held, "public", "up_s")
			c.exec("SET ROLE up_o", "GRANT SELECT,UPDATE ON SEQUENCE public.up_s TO up_r", "RESET ROLE")
			c.check(held, "public", "up_s")
			c.exec("DROP SEQUENCE public.up_s")
			c.check(held, "public", "up_s")
		})
	})
	t.Run("sequence_default_privileges", func(t *testing.T) {
		upScenario(t, func(c *upCase) {
			c.seed()
			c.exec("GRANT CREATE ON SCHEMA public TO up_o", "ALTER DEFAULT PRIVILEGES FOR ROLE up_o GRANT USAGE ON SEQUENCES TO up_r")
			// Revoke the cluster-level default even when an assertion fails.
			c.t.Cleanup(func() {
				_, err := c.b.DB.ExecContext(c.p.Ctx, "ALTER DEFAULT PRIVILEGES FOR ROLE up_o REVOKE USAGE ON SEQUENCES FROM up_r")
				if err != nil {
					c.t.Error(err)
				}
			})
			c.exec("SET ROLE up_o", "CREATE SEQUENCE public.up_default", "RESET ROLE")
			c.sequenceACL("public.up_default", "nonempty")
			c.check(c.conn, "public", "up_default", c.familyRow("up_o", "up_o", "public", "up_default", "SEQUENCE", "YES"), c.familyRow("up_o", "up_r", "public", "up_default", "SEQUENCE", "NO"))
			c.exec("ALTER DEFAULT PRIVILEGES FOR ROLE up_o REVOKE USAGE ON SEQUENCES FROM up_r")
		})
	})
	t.Run("foreign_objects", func(t *testing.T) {
		upScenario(t, func(c *upCase) {
			c.seed()
			c.t.Cleanup(func() {
				for _, q := range []string{"DROP SERVER IF EXISTS up_server CASCADE", "DROP FOREIGN DATA WRAPPER IF EXISTS up_wrapper CASCADE"} {
					if _, err := c.b.DB.ExecContext(c.p.Ctx, q); err != nil {
						c.t.Error(err)
					}
				}
			})
			// Wrapper ownership requires a superuser; keep ordinary reader grants separate.
			c.exec("CREATE FOREIGN DATA WRAPPER up_wrapper", "ALTER FOREIGN DATA WRAPPER up_wrapper OWNER TO up_power", "CREATE SERVER up_server FOREIGN DATA WRAPPER up_wrapper", "ALTER SERVER up_server OWNER TO up_power")
			held := c.hold(c.conn, upQuery)
			for _, tc := range []struct{ name, kind, family string }{{"up_wrapper", "FOREIGN DATA WRAPPER", "FOREIGN DATA WRAPPER"}, {"up_server", "FOREIGN SERVER", "FOREIGN SERVER"}} {
				own := c.familyRow("up_power", "up_power", "", tc.name, tc.family, "YES")
				raw := "SELECT fdwacl IS NULL AS absent,COALESCE(cardinality(fdwacl),0)::bigint AS entries FROM pg_foreign_data_wrapper WHERE fdwname=$1"
				empty := "SELECT fdwacl IS NOT NULL AS explicit,cardinality(fdwacl)::bigint AS entries FROM pg_foreign_data_wrapper WHERE fdwname=$1"
				if tc.name == "up_server" {
					raw = "SELECT srvacl IS NULL AS absent,COALESCE(cardinality(srvacl),0)::bigint AS entries FROM pg_foreign_server WHERE srvname=$1"
					empty = "SELECT srvacl IS NOT NULL AS explicit,cardinality(srvacl)::bigint AS entries FROM pg_foreign_server WHERE srvname=$1"
				}
				c.run(c.conn, fixture.Select(raw, []string{"absent", "entries"}, []string{"BOOL", "INT8"}, [][]any{{true, int64(0)}}, tc.name))
				c.actor("up_power", func() { c.check(held, "", tc.name, own) })
				c.actor("up_r", func() { c.check(held, "", tc.name) })
				c.exec("SET ROLE up_power", "GRANT USAGE ON "+tc.kind+" "+tc.name+" TO PUBLIC", "RESET ROLE")
				c.actor("up_p", func() { c.check(held, "", tc.name, c.familyRow("up_power", "PUBLIC", "", tc.name, tc.family, "NO")) })
				c.exec("SET ROLE up_power", "REVOKE USAGE ON "+tc.kind+" "+tc.name+" FROM PUBLIC", "RESET ROLE")
				for _, transition := range []struct{ sql, option string }{{"GRANT USAGE ON " + tc.kind + " " + tc.name + " TO up_r", "NO"}, {"GRANT USAGE ON " + tc.kind + " " + tc.name + " TO up_r WITH GRANT OPTION", "YES"}, {"REVOKE GRANT OPTION FOR USAGE ON " + tc.kind + " " + tc.name + " FROM up_r", "NO"}} {
					c.exec("SET ROLE up_power", transition.sql, "RESET ROLE")
					reader := c.familyRow("up_power", "up_r", "", tc.name, tc.family, transition.option)
					c.check(held, "", tc.name, own, reader)
					c.actor("up_r", func() { c.check(held, "", tc.name, reader) })
				}
				c.exec("SET ROLE up_power", "REVOKE ALL ON "+tc.kind+" "+tc.name+" FROM up_r,up_power", "RESET ROLE")
				c.check(held, "", tc.name)
				c.run(c.conn, fixture.Select(empty, []string{"explicit", "entries"}, []string{"BOOL", "INT8"}, [][]any{{true, int64(0)}}, tc.name))
			}
			c.exec("DROP SERVER up_server", "DROP FOREIGN DATA WRAPPER up_wrapper")
			c.check(held, "", "up_server")
			c.check(held, "", "up_wrapper")
		})
	})
	t.Run("collation_and_exclusions", func(t *testing.T) {
		upScenario(t, func(c *upCase) {
			c.seed()
			c.exec(`CREATE COLLATION public.up_c FROM pg_catalog."C"`, "ALTER COLLATION public.up_c OWNER TO up_o", "CREATE TYPE public.up_composite AS(a integer)", "CREATE TYPE public.up_enum AS ENUM ('a')", "CREATE TYPE public.up_range AS RANGE(subtype=integer)", "CREATE TABLE public.up_table(a integer)", "CREATE FUNCTION public.up_function() RETURNS integer LANGUAGE SQL AS 'SELECT 1'")
			held := c.hold(c.conn, upQuery)
			c.actor("up_o", func() {
				c.check(held, "public", "up_c", c.familyRow("up_o", "PUBLIC", "public", "up_c", "COLLATION", "NO"))
			})
			c.actor("up_r", func() {
				c.check(held, "public", "up_c", c.familyRow("up_o", "PUBLIC", "public", "up_c", "COLLATION", "NO"))
			})
			for _, name := range []string{"up_composite", "_up_composite", "up_enum", "_up_enum", "up_range", "up_table", "up_function", "up_missing"} {
				c.check(held, "public", name)
			}
			c.exec("DROP COLLATION public.up_c")
			c.check(held, "public", "up_c")
		})
	})
}
