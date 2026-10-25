package reference_test

import (
	"testing"

	"stuntgres/reference/fixture"
)

func TestReferenceInfoRoutinePrivilegesACL(t *testing.T) {
	t.Run("held_peer", func(t *testing.T) {
		rpScenario(t, func(c *rrgCase) {
			rpSeed(c)
			peer := c.peer()
			public := rpOrigin(c, "rp_o", "PUBLIC", "NO")
			c.exec("SET SESSION AUTHORIZATION rp_r")
			held := c.hold(c.conn, rpQuery)
			rpRead(c, held, public)
			c.run(peer, fixture.Exec("SET ROLE rp_o", 0))
			c.run(peer, fixture.Exec("REVOKE EXECUTE ON FUNCTION public.rp_f() FROM PUBLIC", 0))
			rpRead(c, held)
			for _, phase := range []struct{ query, option string }{
				{"GRANT EXECUTE ON FUNCTION public.rp_f() TO rp_r", "NO"},
				{"GRANT EXECUTE ON FUNCTION public.rp_f() TO rp_r WITH GRANT OPTION", "YES"},
				{"REVOKE GRANT OPTION FOR EXECUTE ON FUNCTION public.rp_f() FROM rp_r", "NO"},
				{"REVOKE EXECUTE ON FUNCTION public.rp_f() FROM rp_r", ""},
			} {
				c.run(peer, fixture.Exec(phase.query, 0))
				var want [][]any
				if phase.option != "" {
					want = [][]any{rpOrigin(c, "rp_o", "rp_r", phase.option)}
				}
				rpRead(c, held, want...)
			}
			c.exec("RESET SESSION AUTHORIZATION")
		})
	})
	t.Run("explicit_empty", func(t *testing.T) {
		rpScenario(t, func(c *rrgCase) {
			rpSeed(c)
			c.exec("SET ROLE rp_o", "REVOKE ALL ON FUNCTION public.rp_f() FROM PUBLIC,rp_o", "RESET ROLE")
			held := c.hold(c.conn, rpQuery)
			rpRead(c, held)
			c.actor("rp_o", func() {
				rpRead(c, held)
				c.exec("GRANT EXECUTE ON FUNCTION public.rp_f() TO rp_o")
				rpRead(c, held, rpOrigin(c, "rp_o", "rp_o", "YES"))
			})
		})
	})
	t.Run("distinct_grantors_restrict_cascade", func(t *testing.T) {
		rpScenario(t, func(c *rrgCase) {
			rpSeed(c)
			c.exec("SET ROLE rp_o", "REVOKE EXECUTE ON FUNCTION public.rp_f() FROM PUBLIC", "GRANT EXECUTE ON FUNCTION public.rp_f() TO rp_d,rp_g WITH GRANT OPTION", "RESET ROLE")
			c.actor("rp_d", func() { c.exec("GRANT EXECUTE ON FUNCTION public.rp_f() TO rp_r") })
			c.actor("rp_g", func() { c.exec("GRANT EXECUTE ON FUNCTION public.rp_f() TO rp_r") })
			d, g := rpOrigin(c, "rp_d", "rp_r", "NO"), rpOrigin(c, "rp_g", "rp_r", "NO")
			held := c.hold(c.conn, rpQuery)
			c.actor("rp_r", func() { rpRead(c, held, d, g) })
			owner := rpOrigin(c, "rp_o", "rp_o", "YES")
			all := [][]any{owner, rpOrigin(c, "rp_o", "rp_d", "YES"), rpOrigin(c, "rp_o", "rp_g", "YES"), d, g}
			rpRead(c, held, all...)
			c.exec("SET ROLE rp_o")
			s := fixture.ImplicitError("REVOKE GRANT OPTION FOR EXECUTE ON FUNCTION public.rp_f() FROM rp_d RESTRICT", fixture.Diagnostic{Code: "2BP01", Message: "dependent privileges exist", Hint: "Use CASCADE to revoke them too."})
			s.ErrorStage = "execute"
			c.run(c.conn, s)
			c.exec("RESET ROLE")
			rpRead(c, held, all...)
			c.exec("SET ROLE rp_o", "REVOKE GRANT OPTION FOR EXECUTE ON FUNCTION public.rp_f() FROM rp_d CASCADE", "RESET ROLE")
			rpRead(c, held, owner, rpOrigin(c, "rp_o", "rp_d", "NO"), rpOrigin(c, "rp_o", "rp_g", "YES"), g)
			c.actor("rp_r", func() { rpRead(c, held, g) })
		})
	})
}
