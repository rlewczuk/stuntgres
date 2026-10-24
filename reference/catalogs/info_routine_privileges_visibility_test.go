package reference_test

import (
	"testing"

	"stuntgres/reference/fixture"
)

func TestReferenceInfoRoutinePrivilegesVisibility(t *testing.T) {
	t.Run("public_and_enabled_origins", func(t *testing.T) {
		rpScenario(t, func(c *rrgCase) {
			rpSeed(c)
			baseline := c.baseline("public.rp_f()")
			public := baseline[1]
			held := c.hold(c.conn, rpQuery)
			c.actor("rp_x", func() { rpRead(c, held, public); rpRoleView(c) })
			for _, actor := range []string{"rp_o", "rp_power"} {
				c.actor(actor, func() { rpRead(c, held, baseline...); rpRoleView(c, baseline...) })
			}
			c.exec("GRANT rp_o TO rp_m WITH INHERIT TRUE")
			c.actor("rp_m", func() { rpRead(c, held, baseline...); rpRoleView(c, baseline...) })
			c.exec("GRANT rp_o TO rp_m WITH INHERIT FALSE, SET TRUE", "GRANT rp_power TO rp_m WITH INHERIT TRUE, SET FALSE")
			c.actor("rp_m", func() { rpRead(c, held, public); rpRoleView(c) })
			c.exec("SET ROLE rp_o", "GRANT EXECUTE ON FUNCTION public.rp_f() TO rp_r,rp_g", "RESET ROLE")
			row := rpOrigin(c, "rp_o", "rp_r", "NO")
			c.actor("rp_r", func() { rpRead(c, held, public, row); rpRoleView(c, row) })
			c.actor("rp_power", func() {
				rpRead(c, held, append(append([][]any(nil), baseline...), row, rpOrigin(c, "rp_o", "rp_g", "NO"))...)
			})
			c.actor("rp_m", func() { rpRead(c, held, public) })
			c.exec("SET ROLE rp_o", "REVOKE EXECUTE ON FUNCTION public.rp_f() FROM PUBLIC", "RESET ROLE")
			c.actor("rp_x", func() { rpRead(c, held) })
			c.actor("rp_r", func() { rpRead(c, held, row) })
			// No execute right is required to observe origins one granted.
			c.exec("SET ROLE rp_o", "REVOKE EXECUTE ON FUNCTION public.rp_f() FROM rp_o", "RESET ROLE")
			c.actor("rp_o", func() { rpRead(c, held, row, rpOrigin(c, "rp_o", "rp_g", "NO")) })
		})
	})
	t.Run("membership_paths_set_and_diamond", func(t *testing.T) {
		rpScenario(t, func(c *rrgCase) {
			rpSeed(c)
			c.exec("SET ROLE rp_o", "REVOKE EXECUTE ON FUNCTION public.rp_f() FROM PUBLIC", "GRANT EXECUTE ON FUNCTION public.rp_f() TO rp_g", "RESET ROLE", "GRANT rp_g TO rp_m WITH INHERIT FALSE, SET TRUE")
			held := c.hold(c.conn, rpQuery)
			row := rpOrigin(c, "rp_o", "rp_g", "NO")
			read := func(want ...[]any) { c.actor("rp_m", func() { rpRead(c, held, want...) }) }
			read()
			c.actor("rp_m", func() {
				c.exec("SET ROLE rp_g")
				rpRead(c, held, row)
				c.exec("RESET ROLE")
				rpRead(c, held)
			})
			c.exec("GRANT rp_g TO rp_m WITH ADMIN TRUE, INHERIT FALSE, SET FALSE")
			read()
			c.exec("GRANT rp_x TO rp_m WITH INHERIT TRUE", "GRANT rp_y TO rp_m WITH INHERIT TRUE", "GRANT rp_g TO rp_x WITH INHERIT TRUE", "GRANT rp_g TO rp_y WITH INHERIT TRUE")
			read(row)
			c.exec("REVOKE rp_g FROM rp_x")
			read(row)
			c.exec("REVOKE rp_g FROM rp_y")
			read()
			// Reused role spelling cannot retain the retired role's ACL origin.
			c.exec("SET ROLE rp_o", "REVOKE EXECUTE ON FUNCTION public.rp_f() FROM rp_g", "RESET ROLE", "REVOKE rp_g FROM rp_m", "DROP ROLE rp_g", "CREATE ROLE rp_g", "GRANT rp_g TO rp_m WITH INHERIT TRUE")
			read()
			c.exec("SET ROLE rp_o", "GRANT EXECUTE ON FUNCTION public.rp_f() TO rp_g", "RESET ROLE")
			read(row)
		})
	})
	t.Run("named_grantee_ownership", func(t *testing.T) {
		rpScenario(t, func(c *rrgCase) {
			rpSeed(c)
			c.exec("SET ROLE rp_o", "GRANT EXECUTE ON FUNCTION public.rp_f() TO rp_r,rp_power", "RESET ROLE")
			baseline := c.baseline("public.rp_f()")
			held := c.hold(c.conn, rpQuery)
			check := func(option string) {
				c.actor("rp_o", func() {
					rpRead(c, held, append(append([][]any(nil), baseline...), rpOrigin(c, "rp_o", "rp_r", option), rpOrigin(c, "rp_o", "rp_power", "YES"))...)
				})
			}
			check("NO")
			c.exec("GRANT rp_o TO rp_r WITH INHERIT TRUE")
			check("YES")
			c.exec("REVOKE rp_o FROM rp_r")
			check("NO")
		})
	})
	t.Run("private_schema", func(t *testing.T) {
		rpScenario(t, func(c *rrgCase) {
			rpSeed(c)
			c.exec("CREATE SCHEMA rp_private AUTHORIZATION rp_o", "SET ROLE rp_o", "CREATE FUNCTION rp_private.rp_hidden() RETURNS integer RETURN 7", "GRANT EXECUTE ON FUNCTION rp_private.rp_hidden() TO rp_r", "RESET ROLE")
			schema, name, _, specific, _ := c.identity("rp_private.rp_hidden()")
			public := c.row("rp_o", "PUBLIC", schema, name, specific, "NO")
			c.actor("rp_x", func() { c.check(c.conn, schema, name, public) })
			c.actor("rp_r", func() {
				c.check(c.conn, schema, name, public, c.row("rp_o", "rp_r", schema, name, specific, "NO"))
				for _, prepared := range []bool{false, true} {
					s := fixture.ImplicitError("SELECT rp_private.rp_hidden()", fixture.Diagnostic{Code: "42501", Message: "permission denied for schema rp_private", Position: fixture.Position(8)})
					s.SelectRows, s.Prepared, s.ErrorStage = true, prepared, "execute"
					if prepared {
						s.ErrorStage = "prepare"
					}
					c.run(c.conn, s)
				}
			})
		})
	})
}
