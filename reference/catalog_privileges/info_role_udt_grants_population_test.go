package reference_test

import (
	"stuntgres/reference/fixture"
	"testing"
)

func TestReferenceInfoRoleUdtGrantsPopulation(t *testing.T) {
	t.Run("composite_classes", func(t *testing.T) {
		ugScenario(t, func(c *ugCase) {
			c.seed()
			c.t.Cleanup(func() {
				for _, q := range []string{"DROP FOREIGN TABLE IF EXISTS public.ug_foreign", "DROP SERVER IF EXISTS ug_server", "DROP FOREIGN DATA WRAPPER IF EXISTS ug_fdw"} {
					if _, err := c.conn.ExecContext(c.p.Ctx, q); err != nil {
						c.t.Error(err)
					}
				}
			})
			c.exec("CREATE TYPE ug_zero AS ()", "CREATE TABLE ug_table(a integer)", "CREATE TABLE ug_table_zero()", "CREATE VIEW ug_view AS SELECT a FROM ug_table", "CREATE MATERIALIZED VIEW ug_mat AS SELECT a FROM ug_table WITH NO DATA", "CREATE TABLE ug_parent(a integer) PARTITION BY RANGE(a)", "CREATE TABLE ug_child PARTITION OF ug_parent FOR VALUES FROM(0) TO(10)", "CREATE FOREIGN DATA WRAPPER ug_fdw NO HANDLER NO VALIDATOR", "CREATE SERVER ug_server FOREIGN DATA WRAPPER ug_fdw", "CREATE FOREIGN TABLE ug_foreign(a integer) SERVER ug_server")
			for _, name := range []string{"ug_zero", "ug_table", "ug_table_zero", "ug_view", "ug_mat", "ug_parent", "ug_child", "ug_foreign"} {
				c.check(c.conn, "public", name, c.baseline(c.owner, "public", name)...)
				c.exec("GRANT USAGE ON TYPE public." + name + " TO ug_r")
				want := append(c.baseline(c.owner, "public", name), c.row(c.owner, "ug_r", "public", name, "NO"))
				c.check(c.conn, "public", name, want...)
				c.actor("ug_r", func() { c.check(c.conn, "public", name, c.row(c.owner, "ug_r", "public", name, "NO")) })
			}
			c.exec("GRANT SELECT ON ug_table TO ug_d")
			c.actor("ug_d", func() { c.check(c.conn, "public", "ug_table") })
			c.exec("REVOKE SELECT ON ug_table FROM ug_d")
			c.actor("ug_r", func() { c.check(c.conn, "public", "ug_table", c.row(c.owner, "ug_r", "public", "ug_table", "NO")) })
			// This lookup-only catalog row is a real reciprocal composite, not an
			// uninstalled reserved type identity. Do not mutate its shared ACL.
			var owner string
			q := "SELECT pg_get_userbyid(t.typowner) FROM pg_class c JOIN pg_type t ON t.oid=c.reltype AND t.typrelid=c.oid WHERE c.oid='pg_catalog.pg_class'::regclass"
			if err := c.conn.QueryRowContext(c.p.Ctx, q).Scan(&owner); err != nil {
				c.t.Fatal(err)
			}
			c.check(c.conn, "pg_catalog", "pg_class", c.baseline(owner, "pg_catalog", "pg_class")...)
		})
	})
	t.Run("noncomposites", func(t *testing.T) {
		ugScenario(t, func(c *ugCase) {
			c.seed()
			c.exec("CREATE TYPE ug_enum AS ENUM('a')", "CREATE DOMAIN ug_domain AS integer", "CREATE DOMAIN ug_composite_domain AS public.ug_t", "CREATE TYPE ug_range AS RANGE(subtype=integer,multirange_type_name=ug_multirange)")
			// REL_18_6 ExecGrant_Type_check rejects dependent-type ACLs: arrays
			// use their element type's privileges; multiranges use their range's.
			c.exec("GRANT USAGE ON TYPE ug_enum,ug_domain,ug_composite_domain,ug_range,ug_t TO ug_r")
			for _, prepared := range []bool{false, true} {
				s := fixture.ImplicitError("GRANT USAGE ON TYPE public.ug_multirange TO ug_r", fixture.Diagnostic{Code: "0LP01", Message: "cannot set privileges of multirange types", Hint: "Set the privileges of the range type instead."})
				s.Prepared = prepared
				s.ErrorStage = "execute"
				c.run(c.conn, s)
			}
			c.actor("ug_r", func() { c.check(c.conn, "public", "ug_t", c.row("ug_o", "ug_r", "public", "ug_t", "NO")) })
			for _, name := range []string{"ug_enum", "ug_domain", "ug_composite_domain", "ug_range", "ug_multirange", "_ug_t", "_ug_enum", "_ug_domain", "_ug_range", "_ug_multirange", "ug_missing"} {
				c.check(c.conn, "public", name)
				c.actor("ug_r", func() { c.check(c.conn, "public", name) })
			}
		})
	})
	t.Run("schema_boundary", func(t *testing.T) {
		ugScenario(t, func(c *ugCase) {
			c.seed()
			c.exec("CREATE SCHEMA ug_private AUTHORIZATION ug_o", "CREATE TYPE ug_private.ug_hidden AS(a integer)", "ALTER TYPE ug_private.ug_hidden OWNER TO ug_o", "SET ROLE ug_o", "REVOKE USAGE ON TYPE ug_private.ug_hidden FROM PUBLIC", "GRANT USAGE ON TYPE ug_private.ug_hidden TO ug_r", "RESET ROLE")
			c.exec("GRANT CREATE ON SCHEMA public TO ug_r")
			c.actor("ug_r", func() {
				c.check(c.conn, "ug_private", "ug_hidden", c.row("ug_o", "ug_r", "ug_private", "ug_hidden", "NO"))
				for _, prepared := range []bool{false, true} {
					s := fixture.ImplicitError("CREATE TABLE public.ug_denied(a ug_private.ug_hidden)", fixture.Diagnostic{Code: "42501", Message: "permission denied for schema ug_private", Position: fixture.Position(33)})
					s.Prepared = prepared
					s.ErrorStage = "execute"
					c.run(c.conn, s)
				}
			})
		})
	})
	t.Run("other_session_temp", func(t *testing.T) {
		ugScenario(t, func(c *ugCase) {
			c.seed()
			peer := c.peer()
			c.run(peer, fixture.Exec("CREATE TEMP TABLE ug_temp_peer(a integer)", 0))
			c.t.Cleanup(func() {
				if _, err := peer.ExecContext(c.p.Ctx, "DROP TABLE IF EXISTS ug_temp_peer"); err != nil {
					c.t.Error(err)
				}
			})
			var schema string
			if err := peer.QueryRowContext(c.p.Ctx, "SELECT n.nspname FROM pg_class c JOIN pg_namespace n ON n.oid=c.relnamespace WHERE c.oid='ug_temp_peer'::regclass").Scan(&schema); err != nil {
				c.t.Fatal(err)
			}
			c.check(c.conn, schema, "ug_temp_peer", c.baseline(c.owner, schema, "ug_temp_peer")...)
		})
	})
}
