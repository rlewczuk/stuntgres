package reference_test

import (
	"stuntgres/reference/fixture"
	"testing"
)

func TestReferenceInfoRoleRoutineGrantsACL(t *testing.T) {
	t.Run("held_peer", func(t *testing.T) {
		rrgScenario(t, func(c *rrgCase) {
			c.seed()
			peer := c.peer()
			c.exec("SET SESSION AUTHORIZATION rrg_r")
			held := c.hold(c.conn, rrgQuery)
			c.check(held, "public", "rrg_f")
			c.run(peer, fixture.Exec("SET ROLE rrg_o", 0))
			c.run(peer, fixture.Exec("REVOKE EXECUTE ON FUNCTION public.rrg_f() FROM PUBLIC", 0))
			for _, phase := range []struct{ q, option string }{
				{"GRANT EXECUTE ON FUNCTION public.rrg_f() TO rrg_r", "NO"},
				{"GRANT EXECUTE ON FUNCTION public.rrg_f() TO rrg_r WITH GRANT OPTION", "YES"},
				{"REVOKE GRANT OPTION FOR EXECUTE ON FUNCTION public.rrg_f() FROM rrg_r", "NO"},
				{"REVOKE EXECUTE ON FUNCTION public.rrg_f() FROM rrg_r", ""},
			} {
				c.run(peer, fixture.Exec(phase.q, 0))
				var want [][]any
				if phase.option != "" {
					want = [][]any{c.origin("rrg_o", "rrg_r", phase.option)}
				}
				c.check(held, "public", "rrg_f", want...)
				c.check(c.conn, "public", "rrg_f", want...)
			}
			c.exec("RESET SESSION AUTHORIZATION")
			c.run(peer, fixture.Exec("GRANT EXECUTE ON FUNCTION public.rrg_f() TO rrg_g", 0))
			c.run(peer, fixture.Exec("RESET ROLE", 0))
			c.exec("SET SESSION AUTHORIZATION rrg_m")
			row := c.origin("rrg_o", "rrg_g", "NO")
			c.check(held, "public", "rrg_f")
			c.run(peer, fixture.Exec("GRANT rrg_g TO rrg_m WITH INHERIT TRUE", 0))
			c.check(held, "public", "rrg_f", row)
			c.run(peer, fixture.Exec("REVOKE rrg_g FROM rrg_m", 0))
			c.check(held, "public", "rrg_f")
			c.exec("RESET SESSION AUTHORIZATION")
		})
	})
	t.Run("explicit_empty", func(t *testing.T) {
		rrgScenario(t, func(c *rrgCase) {
			c.seed()
			c.exec("SET ROLE rrg_o", "REVOKE ALL ON FUNCTION public.rrg_f() FROM PUBLIC,rrg_o", "RESET ROLE")
			c.check(c.conn, "public", "rrg_f")
			c.actor("rrg_o", func() {
				c.check(c.conn, "public", "rrg_f")
				c.exec("GRANT ALL ON FUNCTION public.rrg_f() TO rrg_o")
				c.check(c.conn, "public", "rrg_f", c.origin("rrg_o", "rrg_o", "YES"))
			})
		})
	})
	t.Run("delegation_and_cascade", func(t *testing.T) {
		rrgScenario(t, func(c *rrgCase) {
			c.seed()
			c.exec("SET ROLE rrg_o", "GRANT EXECUTE ON FUNCTION public.rrg_f() TO rrg_d,rrg_g WITH GRANT OPTION", "RESET ROLE")
			c.actor("rrg_d", func() { c.exec("GRANT EXECUTE ON FUNCTION public.rrg_f() TO rrg_r") })
			c.actor("rrg_g", func() { c.exec("GRANT EXECUTE ON FUNCTION public.rrg_f() TO rrg_r") })
			d, g := c.origin("rrg_d", "rrg_r", "NO"), c.origin("rrg_g", "rrg_r", "NO")
			c.actor("rrg_r", func() { c.check(c.conn, "public", "rrg_f", d, g) })
			c.actor("rrg_d", func() { c.check(c.conn, "public", "rrg_f", c.origin("rrg_o", "rrg_d", "YES"), d) })
			baseline := c.baseline("public.rrg_f()")
			all := append(baseline, c.origin("rrg_o", "rrg_d", "YES"), c.origin("rrg_o", "rrg_g", "YES"), d, g)
			c.check(c.conn, "public", "rrg_f", all...)
			c.exec("SET ROLE rrg_o")
			s := fixture.ImplicitError("REVOKE GRANT OPTION FOR EXECUTE ON FUNCTION public.rrg_f() FROM rrg_d RESTRICT", fixture.Diagnostic{Code: "2BP01", Message: "dependent privileges exist", Hint: "Use CASCADE to revoke them too."})
			s.ErrorStage = "execute"
			c.run(c.conn, s)
			c.exec("RESET ROLE")
			c.check(c.conn, "public", "rrg_f", all...)
			c.exec("SET ROLE rrg_o", "REVOKE GRANT OPTION FOR EXECUTE ON FUNCTION public.rrg_f() FROM rrg_d CASCADE", "RESET ROLE")
			c.check(c.conn, "public", "rrg_f", append(baseline, c.origin("rrg_o", "rrg_d", "NO"), c.origin("rrg_o", "rrg_g", "YES"), g)...)
			c.actor("rrg_r", func() { c.check(c.conn, "public", "rrg_f", g) })
		})
	})
}
