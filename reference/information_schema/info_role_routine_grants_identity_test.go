package reference_test

import (
	"stuntgres/reference/fixture"
	"testing"
)

func TestReferenceInfoRoleRoutineGrantsIdentity(t *testing.T) {
	t.Run("enabled_origins", func(t *testing.T) {
		rrgScenario(t, func(c *rrgCase) {
			c.seed()
			baseline := c.baseline("public.rrg_f()")
			for _, actor := range []string{"rrg_o", "rrg_power"} {
				c.actor(actor, func() { c.check(c.conn, "public", "rrg_f", baseline...) })
			}
			c.actor("rrg_p", func() {
				c.run(c.conn, fixture.Select("SELECT public.rrg_f() AS result", []string{"result"}, []string{"INT4"}, [][]any{{int64(7)}}))
				c.check(c.conn, "public", "rrg_f")
			})
			c.exec("GRANT rrg_o TO rrg_m WITH INHERIT TRUE")
			c.actor("rrg_m", func() { c.check(c.conn, "public", "rrg_f", baseline...) })
			c.exec("GRANT rrg_o TO rrg_m WITH INHERIT FALSE, SET TRUE", "GRANT rrg_power TO rrg_m WITH INHERIT TRUE, SET FALSE")
			c.actor("rrg_m", func() { c.check(c.conn, "public", "rrg_f") })
			c.exec("SET ROLE rrg_o", "GRANT EXECUTE ON FUNCTION public.rrg_f() TO rrg_r", "RESET ROLE")
			row := c.origin("rrg_o", "rrg_r", "NO")
			c.actor("rrg_r", func() { c.check(c.conn, "public", "rrg_f", row) })
			c.actor("rrg_o", func() { c.check(c.conn, "public", "rrg_f", append(baseline, row)...) })
			c.exec("SET ROLE rrg_o", "REVOKE EXECUTE ON FUNCTION public.rrg_f() FROM rrg_o", "RESET ROLE")
			c.actor("rrg_o", func() { c.check(c.conn, "public", "rrg_f", baseline[1], row) })
		})
	})
	t.Run("schema_independence", func(t *testing.T) {
		rrgScenario(t, func(c *rrgCase) {
			c.seed()
			c.exec("CREATE SCHEMA rrg_private AUTHORIZATION rrg_o", "SET ROLE rrg_o", "CREATE FUNCTION rrg_private.rrg_hidden() RETURNS integer RETURN 7", "GRANT EXECUTE ON FUNCTION rrg_private.rrg_hidden() TO rrg_r", "RESET ROLE")
			schema, name, _, specific, _ := c.identity("rrg_private.rrg_hidden()")
			row := c.row("rrg_o", "rrg_r", schema, name, specific, "NO")
			c.actor("rrg_r", func() {
				c.check(c.conn, schema, name, row)
				for _, prepared := range []bool{false, true} {
					s := fixture.ImplicitError("SELECT rrg_private.rrg_hidden()", fixture.Diagnostic{Code: "42501", Message: "permission denied for schema rrg_private", Position: fixture.Position(8)})
					s.SelectRows = true
					s.ErrorStage = "execute"
					if prepared {
						s = fixture.Prepared(s)
						s.ErrorStage = "prepare"
					}
					c.run(c.conn, s)
				}
			})
			c.actor("rrg_p", func() { c.check(c.conn, schema, name) })
		})
	})
	t.Run("paths_and_set", func(t *testing.T) {
		rrgScenario(t, func(c *rrgCase) {
			c.seed()
			c.exec("SET ROLE rrg_o", "GRANT EXECUTE ON FUNCTION public.rrg_f() TO rrg_g", "RESET ROLE", "GRANT rrg_g TO rrg_m WITH INHERIT FALSE, SET TRUE")
			row := c.origin("rrg_o", "rrg_g", "NO")
			held := c.hold(c.conn, rrgQuery)
			read := func(want ...[]any) {
				c.actor("rrg_m", func() { c.check(held, "public", "rrg_f", want...); c.check(c.conn, "public", "rrg_f", want...) })
			}
			read()
			c.actor("rrg_m", func() {
				c.exec("SET ROLE rrg_g")
				c.check(held, "public", "rrg_f", row)
				c.exec("RESET ROLE")
				c.check(held, "public", "rrg_f")
			})
			c.exec("GRANT rrg_g TO rrg_m WITH ADMIN TRUE, INHERIT FALSE, SET FALSE")
			read()
			c.exec("GRANT rrg_x TO rrg_m WITH INHERIT TRUE", "GRANT rrg_y TO rrg_m WITH INHERIT TRUE", "GRANT rrg_g TO rrg_x WITH INHERIT TRUE", "GRANT rrg_g TO rrg_y WITH INHERIT TRUE")
			read(row)
			c.exec("REVOKE rrg_g FROM rrg_x")
			read(row)
			c.exec("REVOKE rrg_g FROM rrg_y")
			read()
			c.exec("SET ROLE rrg_o", "REVOKE EXECUTE ON FUNCTION public.rrg_f() FROM rrg_g", "RESET ROLE", "REVOKE rrg_g FROM rrg_m", "DROP ROLE rrg_g", "CREATE ROLE rrg_g", "GRANT rrg_g TO rrg_m WITH INHERIT TRUE")
			read()
			c.exec("SET ROLE rrg_o", "GRANT EXECUTE ON FUNCTION public.rrg_f() TO rrg_g", "RESET ROLE")
			read(row)
		})
	})
	t.Run("grantee_ownership", func(t *testing.T) {
		rrgScenario(t, func(c *rrgCase) {
			c.seed()
			c.exec("SET ROLE rrg_o", "GRANT EXECUTE ON FUNCTION public.rrg_f() TO rrg_r,rrg_power", "RESET ROLE")
			baseline := c.baseline("public.rrg_f()")
			check := func(option string) {
				c.actor("rrg_o", func() {
					c.check(c.conn, "public", "rrg_f", append(baseline, c.origin("rrg_o", "rrg_r", option), c.origin("rrg_o", "rrg_power", "YES"))...)
				})
			}
			check("NO")
			c.exec("GRANT rrg_o TO rrg_r WITH INHERIT TRUE")
			check("YES")
			c.exec("REVOKE rrg_o FROM rrg_r")
			check("NO")
		})
	})
	t.Run("implicit_database_owner", func(t *testing.T) {
		rrgScenario(t, func(c *rrgCase) {
			c.seed()
			c.exec("SET ROLE rrg_o", "GRANT EXECUTE ON FUNCTION public.rrg_f() TO pg_database_owner", "RESET ROLE")
			var owner string
			if err := c.conn.QueryRowContext(c.p.Ctx, "SELECT pg_get_userbyid(datdba) FROM pg_database WHERE datname=current_database()").Scan(&owner); err != nil {
				c.t.Fatal(err)
			}
			c.exec("GRANT " + owner + " TO rrg_m WITH INHERIT TRUE")
			row := c.origin("rrg_o", "pg_database_owner", "NO")
			c.actor("rrg_m", func() { c.check(c.conn, "public", "rrg_f", row) })
			c.exec("REVOKE " + owner + " FROM rrg_m")
			c.actor("rrg_m", func() { c.check(c.conn, "public", "rrg_f") })
		})
	})
}
