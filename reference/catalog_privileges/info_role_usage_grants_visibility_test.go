package reference_test

import (
	"testing"

	"stuntgres/reference/fixture"
)

func TestReferenceInfoRoleUsageGrantsVisibilityBoundaries(t *testing.T) {
	t.Run("denied_schema", func(t *testing.T) {
		rugScenario(t, func(c *rugCase) {
			c.seed()
			c.exec("CREATE SCHEMA rug_private AUTHORIZATION rug_o", "SET ROLE rug_o", "CREATE DOMAIN rug_private.rug_d AS integer", "REVOKE USAGE ON DOMAIN rug_private.rug_d FROM PUBLIC", "GRANT USAGE ON DOMAIN rug_private.rug_d TO rug_r", "CREATE SEQUENCE rug_private.rug_s", "GRANT USAGE ON SEQUENCE rug_private.rug_s TO rug_r", "RESET ROLE")
			held := c.hold(c.conn, rugQuery)
			c.actor("rug_r", func() {
				for _, tc := range []struct{ name, family string }{{"rug_d", "DOMAIN"}, {"rug_s", "SEQUENCE"}} {
					row := c.familyRow("rug_o", "rug_r", "rug_private", tc.name, tc.family, "NO")
					c.check(c.conn, "rug_private", tc.name, row)
					c.check(held, "rug_private", tc.name, row)
				}
				for _, q := range []string{"SELECT NULL::rug_private.rug_d", "SELECT nextval('rug_private.rug_s')"} {
					s := fixture.ImplicitError(q, fixture.Diagnostic{Code: "42501", Message: "permission denied for schema rug_private", Position: fixture.Position(14)})
					if q == "SELECT nextval('rug_private.rug_s')" { s = fixture.ImplicitError(q, fixture.Diagnostic{Code: "42501", Message: "permission denied for schema rug_private", Position: fixture.Position(16)}) }
					s.SelectRows = true
					c.run(c.conn, s)
				}
			})
			c.actor("rug_x", func() { c.check(held, "rug_private", "rug_d"); c.check(held, "rug_private", "rug_s") })
		})
	})
	t.Run("identity_sequences", func(t *testing.T) {
		rugScenario(t, func(c *rugCase) {
			c.seed()
			c.exec("CREATE TABLE public.rug_identity(id bigint GENERATED ALWAYS AS IDENTITY)", "ALTER TABLE public.rug_identity OWNER TO rug_o")
			var name string
			if err := c.conn.QueryRowContext(c.p.Ctx, "SELECT c.relname FROM pg_class c JOIN pg_namespace n ON n.oid=c.relnamespace WHERE n.nspname='public' AND c.relname='rug_identity_id_seq'").Scan(&name); err != nil { c.t.Fatal(err) }
			held := c.hold(c.conn, rugQuery)
			c.sequenceACL("public."+name, "absent")
			c.check(held, "public", name)
			c.exec("SET ROLE rug_o", "GRANT USAGE ON SEQUENCE public."+name+" TO rug_r", "RESET ROLE")
			c.actor("rug_r", func() { c.check(held, "public", name, c.familyRow("rug_o", "rug_r", "public", name, "SEQUENCE", "NO")) })
		})
	})
	t.Run("peer_temporary_sequence", func(t *testing.T) {
		rugScenario(t, func(c *rugCase) {
			c.seed()
			peer := c.peer()
			for _, q := range []string{"SET ROLE rug_o", "CREATE TEMP SEQUENCE rug_temp_s", "GRANT USAGE ON SEQUENCE rug_temp_s TO rug_r", "RESET ROLE"} { c.run(peer, fixture.Exec(q, 0)) }
			var schema string
			if err := peer.QueryRowContext(c.p.Ctx, "SELECT n.nspname FROM pg_class c JOIN pg_namespace n ON n.oid=c.relnamespace WHERE c.relname='rug_temp_s' AND n.oid=pg_my_temp_schema()").Scan(&schema); err != nil { c.t.Fatal(err) }
			held := c.hold(c.conn, rugQuery)
			c.actor("rug_r", func() {
				row := c.familyRow("rug_o", "rug_r", schema, "rug_temp_s", "SEQUENCE", "NO")
				c.check(c.conn, schema, "rug_temp_s", row)
				c.check(held, schema, "rug_temp_s", row)
			})
			c.run(peer, fixture.Exec("DROP SEQUENCE "+schema+".rug_temp_s", 0))
			c.check(held, schema, "rug_temp_s")
		})
	})
}
