package reference_test

import (
	"testing"
	"unicode/utf8"

	"stuntgres/reference/fixture"
)

func TestReferenceInfoRoutinePrivilegesDiagnostics(t *testing.T) {
	t.Run("read_position_and_stage", func(t *testing.T) {
		rpScenario(t, func(c *rrgCase) {
			for _, prefix := range []string{"", "/* λ雪 */ "} {
				for _, prepared := range []bool{false, true} {
					s := fixture.ImplicitError(prefix+"SELECT missing FROM information_schema.routine_privileges", fixture.Diagnostic{Code: "42703", Message: `column "missing" does not exist`, Position: fixture.Position(utf8.RuneCountInString(prefix) + 8)})
					s.SelectRows, s.Prepared, s.ErrorStage = true, prepared, "execute"
					if prepared {
						s.ErrorStage = "prepare"
					}
					c.run(c.conn, s)
				}
			}
		})
	})
	for _, prepared := range []bool{false, true} {
		name := "direct"
		if prepared {
			name = "prepared"
		}
		t.Run(name, func(t *testing.T) {
			rpScenario(t, func(c *rrgCase) {
				rpSeed(c)
				c.exec("SET ROLE rp_o", "REVOKE EXECUTE ON FUNCTION public.rp_f() FROM PUBLIC", "GRANT EXECUTE ON FUNCTION public.rp_f() TO rp_r", "RESET ROLE")
				held := c.hold(c.conn, rpQuery)
				prior := [][]any{rpOrigin(c, "rp_o", "rp_o", "YES"), rpOrigin(c, "rp_o", "rp_r", "NO")}
				unchanged := func() { rpRead(c, held, prior...) }
				for _, tc := range []struct{ query, code, message string }{
					{"GRANT EXECUTE ON FUNCTION public.rp_f() TO rp_missing", "42704", `role "rp_missing" does not exist`},
					{"REVOKE EXECUTE ON FUNCTION public.rp_f() FROM rp_missing", "42704", `role "rp_missing" does not exist`},
					{"GRANT EXECUTE ON FUNCTION public.rp_f() TO PUBLIC WITH GRANT OPTION", "0LP01", "grant options can only be granted to roles"},
					{"GRANT EXECUTE ON FUNCTION public.rp_missing() TO rp_r", "42883", "function public.rp_missing() does not exist"},
					{"REVOKE EXECUTE ON FUNCTION public.rp_missing() FROM rp_r", "42883", "function public.rp_missing() does not exist"},
					{"GRANT EXECUTE ON FUNCTION public.rp_f() TO rp_d,rp_missing", "42704", `role "rp_missing" does not exist`},
					{"REVOKE EXECUTE ON FUNCTION public.rp_f() FROM rp_r,rp_missing", "42704", `role "rp_missing" does not exist`},
					{"GRANT EXECUTE ON FUNCTION public.rp_f(),public.rp_missing() TO rp_d", "42883", "function public.rp_missing() does not exist"},
					{"REVOKE EXECUTE ON FUNCTION public.rp_f(),public.rp_missing() FROM rp_r", "42883", "function public.rp_missing() does not exist"},
				} {
					c.exec("SET ROLE rp_o")
					s := fixture.ImplicitError(tc.query, fixture.Diagnostic{Code: tc.code, Message: tc.message})
					s.Prepared, s.ErrorStage = prepared, "execute"
					c.run(c.conn, s)
					c.exec("RESET ROLE")
					unchanged()
				}
				c.actor("rp_x", func() {
					s := fixture.ImplicitError("GRANT EXECUTE ON FUNCTION public.rp_f() TO rp_d", fixture.Diagnostic{Code: "42501", Message: "permission denied for function rp_f"})
					s.Prepared, s.ErrorStage = prepared, "execute"
					c.run(c.conn, s)
					rpRead(c, held)
				})
				unchanged()
				c.exec("BEGIN", "SET LOCAL ROLE rp_o")
				s := fixture.ImplicitError("GRANT EXECUTE ON FUNCTION public.rp_f() TO rp_missing", fixture.Diagnostic{Code: "42704", Message: `role "rp_missing" does not exist`})
				s.Prepared, s.ErrorStage = prepared, "execute"
				c.run(c.conn, s)
				c.exec("ROLLBACK")
				unchanged()
			})
		})
	}
}
