package reference_test

import (
	"stuntgres/reference/fixture"
	"testing"
)

func TestReferenceInfoUdtPrivilegesPopulation(t *testing.T) {
	t.Run("composite_classes", func(t *testing.T) {
		uvScenario(t, func(c *uvCase) {
			c.seed()
			c.t.Cleanup(func() {
				for _, q := range []string{"DROP FOREIGN TABLE IF EXISTS public.uv_foreign", "DROP SERVER IF EXISTS uv_server", "DROP FOREIGN DATA WRAPPER IF EXISTS uv_fdw"} {
					if _, err := c.conn.ExecContext(c.p.Ctx, q); err != nil {
						c.t.Error(err)
					}
				}
			})
			c.exec("CREATE TYPE uv_zero AS ()", "CREATE TABLE uv_table(a integer)", "CREATE TABLE uv_table_zero()", "CREATE VIEW uv_view AS SELECT a FROM uv_table", "CREATE MATERIALIZED VIEW uv_mat AS SELECT a FROM uv_table WITH NO DATA", "CREATE TABLE uv_parent(a integer) PARTITION BY RANGE(a)", "CREATE TABLE uv_child PARTITION OF uv_parent FOR VALUES FROM(0) TO(10)", "CREATE FOREIGN DATA WRAPPER uv_fdw NO HANDLER NO VALIDATOR", "CREATE SERVER uv_server FOREIGN DATA WRAPPER uv_fdw", "CREATE FOREIGN TABLE uv_foreign(a integer) SERVER uv_server")
			for _, name := range []string{"uv_zero", "uv_table", "uv_table_zero", "uv_view", "uv_mat", "uv_parent", "uv_child", "uv_foreign"} {
				c.check(c.conn, "public", name, c.baseline(c.owner, "public", name)...)
				c.exec("GRANT USAGE ON TYPE public." + name + " TO uv_r")
				want := append(c.baseline(c.owner, "public", name), c.row(c.owner, "uv_r", "public", name, "NO"))
				c.check(c.conn, "public", name, want...)
				c.actor("uv_r", func() {
					c.check(c.conn, "public", name, c.row(c.owner, "PUBLIC", "public", name, "NO"), c.row(c.owner, "uv_r", "public", name, "NO"))
				})
			}
			c.exec("GRANT SELECT ON uv_table TO uv_d")
			c.actor("uv_d", func() { c.check(c.conn, "public", "uv_table", c.row(c.owner, "PUBLIC", "public", "uv_table", "NO")) })
			c.exec("REVOKE SELECT ON uv_table FROM uv_d")
			c.actor("uv_r", func() {
				c.check(c.conn, "public", "uv_table", c.row(c.owner, "PUBLIC", "public", "uv_table", "NO"), c.row(c.owner, "uv_r", "public", "uv_table", "NO"))
			})
			// This lookup-only catalog row is a real reciprocal composite, not an
			// uninstalled reserved type identity. Do not mutate its shared ACL.
			var owner string
			q := "SELECT pg_get_userbyid(t.typowner) FROM pg_class c JOIN pg_type t ON t.oid=c.reltype AND t.typrelid=c.oid WHERE c.oid='pg_catalog.pg_class'::regclass"
			if err := c.conn.QueryRowContext(c.p.Ctx, q).Scan(&owner); err != nil {
				c.t.Fatal(err)
			}
			c.check(c.conn, "pg_catalog", "pg_class", c.baseline(owner, "pg_catalog", "pg_class")...)
			c.actor("uv_p", func() {
				c.check(c.conn, "pg_catalog", "pg_class", c.row(owner, "PUBLIC", "pg_catalog", "pg_class", "NO"))
			})
		})
	})
	t.Run("noncomposites", func(t *testing.T) {
		uvScenario(t, func(c *uvCase) {
			c.seed()
			c.exec("CREATE TYPE uv_enum AS ENUM('a')", "CREATE DOMAIN uv_domain AS integer", "CREATE DOMAIN uv_composite_domain AS public.uv_t", "CREATE TYPE uv_range AS RANGE(subtype=integer,multirange_type_name=uv_multirange)")
			// REL_18_6 ExecGrant_Type_check rejects dependent-type ACLs: arrays
			// use their element type's privileges; multiranges use their range's.
			c.exec("GRANT USAGE ON TYPE uv_enum,uv_domain,uv_composite_domain,uv_range,uv_t TO uv_r")
			for _, prepared := range []bool{false, true} {
				s := fixture.ImplicitError("GRANT USAGE ON TYPE public.uv_multirange TO uv_r", fixture.Diagnostic{Code: "0LP01", Message: "cannot set privileges of multirange types", Hint: "Set the privileges of the range type instead."})
				s.Prepared = prepared
				s.ErrorStage = "execute"
				c.run(c.conn, s)
			}
			c.actor("uv_r", func() { c.check(c.conn, "public", "uv_t", c.public(), c.origin("uv_o", "uv_r", "NO")) })
			for _, name := range []string{"uv_enum", "uv_domain", "uv_composite_domain", "uv_range", "uv_multirange", "_uv_t", "_uv_enum", "_uv_domain", "_uv_range", "_uv_multirange", "uv_missing"} {
				c.check(c.conn, "public", name)
				c.actor("uv_r", func() { c.check(c.conn, "public", name) })
			}
			for _, name := range []string{"int4", "text", "name", "_int4", "int4range", "int4multirange"} {
				c.check(c.conn, "pg_catalog", name)
				c.actor("uv_p", func() { c.check(c.conn, "pg_catalog", name) })
			}
		})
	})
	t.Run("schema_boundary", func(t *testing.T) {
		uvScenario(t, func(c *uvCase) {
			c.seed()
			c.exec("CREATE SCHEMA uv_private AUTHORIZATION uv_o", "CREATE TYPE uv_private.uv_hidden AS(a integer)", "ALTER TYPE uv_private.uv_hidden OWNER TO uv_o", "SET ROLE uv_o", "REVOKE USAGE ON TYPE uv_private.uv_hidden FROM PUBLIC", "GRANT USAGE ON TYPE uv_private.uv_hidden TO uv_r", "RESET ROLE")
			c.actor("uv_p", func() { c.check(c.conn, "uv_private", "uv_hidden") })
			c.exec("SET ROLE uv_o", "GRANT USAGE ON TYPE uv_private.uv_hidden TO PUBLIC", "RESET ROLE")
			c.actor("uv_p", func() {
				c.check(c.conn, "uv_private", "uv_hidden", c.row("uv_o", "PUBLIC", "uv_private", "uv_hidden", "NO"))
			})
			c.exec("SET ROLE uv_o", "REVOKE USAGE ON TYPE uv_private.uv_hidden FROM PUBLIC", "RESET ROLE")
			c.exec("GRANT CREATE ON SCHEMA public TO uv_r")
			c.actor("uv_r", func() {
				c.check(c.conn, "uv_private", "uv_hidden", c.row("uv_o", "uv_r", "uv_private", "uv_hidden", "NO"))
				for _, prepared := range []bool{false, true} {
					s := fixture.ImplicitError("CREATE TABLE public.uv_denied(a uv_private.uv_hidden)", fixture.Diagnostic{Code: "42501", Message: "permission denied for schema uv_private", Position: fixture.Position(33)})
					s.Prepared = prepared
					s.ErrorStage = "execute"
					c.run(c.conn, s)
				}
			})
		})
	})
	t.Run("other_session_temp", func(t *testing.T) {
		uvScenario(t, func(c *uvCase) {
			c.seed()
			peer := c.peer()
			c.run(peer, fixture.Exec("CREATE TEMP TABLE uv_temp_peer(a integer)", 0))
			c.t.Cleanup(func() {
				if _, err := peer.ExecContext(c.p.Ctx, "DROP TABLE IF EXISTS uv_temp_peer"); err != nil {
					c.t.Error(err)
				}
			})
			var schema string
			if err := peer.QueryRowContext(c.p.Ctx, "SELECT n.nspname FROM pg_class c JOIN pg_namespace n ON n.oid=c.relnamespace WHERE c.oid='uv_temp_peer'::regclass").Scan(&schema); err != nil {
				c.t.Fatal(err)
			}
			c.check(c.conn, schema, "uv_temp_peer", c.baseline(c.owner, schema, "uv_temp_peer")...)
			c.actor("uv_p", func() {
				c.check(c.conn, schema, "uv_temp_peer", c.row(c.owner, "PUBLIC", schema, "uv_temp_peer", "NO"))
			})
		})
	})
}
