package reference_test

import (
	"testing"

	"stuntgres/reference/fixture"
)

func TestReferenceInfoUsagePrivilegesVisibility(t *testing.T) {
	t.Run("public_without_enabled_endpoints", func(t *testing.T) {
		upScenario(t, func(c *upCase) {
			c.seed()
			c.exec(`CREATE COLLATION public.up_c FROM pg_catalog."C"`, "ALTER COLLATION public.up_c OWNER TO up_o")
			held := c.hold(c.conn, upQuery)
			public := c.origin("up_o", "PUBLIC", "NO")
			collation := c.familyRow("up_o", "PUBLIC", "public", "up_c", "COLLATION", "NO")
			c.actor("up_r", func() {
				c.check(c.conn, "public", "up_t", public)
				c.check(held, "public", "up_t", public)
				c.check(held, "public", "up_c", collation)
				for _, name := range []string{"up_t", "up_c"} {
					c.run(c.conn, fixture.Prepared(c.step(rugQuery, nil, "public", name)))
				}
			})
			c.actor("up_o", func() {
				c.check(held, "public", "up_t", c.baseline("up_o", "public", "up_t")...)
				c.run(c.conn, c.step(rugQuery, c.baseline("up_o", "public", "up_t"), "public", "up_t"))
			})
			// Owning-role membership alone exposes existing owner origins, not
			// a new object grant bearing the member's name.
			c.exec("GRANT up_o TO up_m WITH INHERIT TRUE")
			c.actor("up_m", func() { c.check(held, "public", "up_t", c.baseline("up_o", "public", "up_t")...) })
		})
	})
	t.Run("denied_schema", func(t *testing.T) {
		upScenario(t, func(c *upCase) {
			c.seed()
			c.exec("CREATE SCHEMA up_private AUTHORIZATION up_o", "SET ROLE up_o", "CREATE DOMAIN up_private.up_d AS integer", "REVOKE USAGE ON DOMAIN up_private.up_d FROM PUBLIC", "GRANT USAGE ON DOMAIN up_private.up_d TO up_r", "CREATE SEQUENCE up_private.up_s", "GRANT USAGE ON SEQUENCE up_private.up_s TO up_r", "RESET ROLE")
			held := c.hold(c.conn, upQuery)
			c.exec("SET ROLE up_o", `CREATE COLLATION up_private.up_c FROM pg_catalog."C"`, "RESET ROLE")
			c.actor("up_r", func() {
				for _, tc := range []struct{ name, family string }{{"up_d", "DOMAIN"}, {"up_s", "SEQUENCE"}} {
					row := c.familyRow("up_o", "up_r", "up_private", tc.name, tc.family, "NO")
					c.check(c.conn, "up_private", tc.name, row)
					c.check(held, "up_private", tc.name, row)
				}
				for _, q := range []string{"SELECT NULL::up_private.up_d", "SELECT nextval('up_private.up_s')"} {
					s := fixture.ImplicitError(q, fixture.Diagnostic{Code: "42501", Message: "permission denied for schema up_private", Position: fixture.Position(14)})
					if q == "SELECT nextval('up_private.up_s')" {
						s = fixture.ImplicitError(q, fixture.Diagnostic{Code: "42501", Message: "permission denied for schema up_private", Position: fixture.Position(16)})
					}
					s.SelectRows = true
					c.run(c.conn, s)
				}
			})
			c.actor("up_x", func() { c.check(held, "up_private", "up_d"); c.check(held, "up_private", "up_s") })
			c.actor("up_x", func() {
				c.check(held, "up_private", "up_c", c.familyRow("up_o", "PUBLIC", "up_private", "up_c", "COLLATION", "NO"))
			})
		})
	})
	t.Run("identity_sequences", func(t *testing.T) {
		upScenario(t, func(c *upCase) {
			c.seed()
			c.exec("CREATE TABLE public.up_identity(id bigint GENERATED ALWAYS AS IDENTITY)", "ALTER TABLE public.up_identity OWNER TO up_o")
			var name string
			if err := c.conn.QueryRowContext(c.p.Ctx, "SELECT c.relname FROM pg_class c JOIN pg_namespace n ON n.oid=c.relnamespace WHERE n.nspname='public' AND c.relname='up_identity_id_seq'").Scan(&name); err != nil {
				c.t.Fatal(err)
			}
			held := c.hold(c.conn, upQuery)
			c.sequenceACL("public."+name, "absent")
			c.check(held, "public", name)
			c.exec("SET ROLE up_o", "GRANT USAGE ON SEQUENCE public."+name+" TO up_r", "RESET ROLE")
			c.actor("up_r", func() { c.check(held, "public", name, c.familyRow("up_o", "up_r", "public", name, "SEQUENCE", "NO")) })
		})
	})
	t.Run("peer_temporary_sequence", func(t *testing.T) {
		upScenario(t, func(c *upCase) {
			c.seed()
			peer := c.peer()
			for _, q := range []string{"SET ROLE up_o", "CREATE TEMP SEQUENCE up_temp_s", "GRANT USAGE ON SEQUENCE up_temp_s TO up_r", "RESET ROLE"} {
				c.run(peer, fixture.Exec(q, 0))
			}
			var schema string
			if err := peer.QueryRowContext(c.p.Ctx, "SELECT n.nspname FROM pg_class c JOIN pg_namespace n ON n.oid=c.relnamespace WHERE c.relname='up_temp_s' AND n.oid=pg_my_temp_schema()").Scan(&schema); err != nil {
				c.t.Fatal(err)
			}
			held := c.hold(c.conn, upQuery)
			c.actor("up_r", func() {
				row := c.familyRow("up_o", "up_r", schema, "up_temp_s", "SEQUENCE", "NO")
				c.check(c.conn, schema, "up_temp_s", row)
				c.check(held, schema, "up_temp_s", row)
			})
			c.run(peer, fixture.Exec("DROP SEQUENCE "+schema+".up_temp_s", 0))
			c.check(held, schema, "up_temp_s")
		})
	})
}
