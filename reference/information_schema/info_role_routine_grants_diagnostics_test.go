package reference_test

import (
	"testing"
	"unicode/utf8"

	"stuntgres/reference/fixture"
)

func TestReferenceInfoRoleRoutineGrantsDiagnostics(t *testing.T) {
	t.Run("read", func(t *testing.T) {
		rrgScenario(t, func(c *rrgCase) {
			for _, prefix := range []string{"", "/* λ雪 */ "} {
				q := prefix + "SELECT missing FROM information_schema.role_routine_grants"
				for _, prepared := range []bool{false, true} {
					s := fixture.ImplicitError(q, fixture.Diagnostic{Code: "42703", Message: `column "missing" does not exist`, Position: fixture.Position(utf8.RuneCountInString(prefix) + 8)})
					s.SelectRows = true
					s.ErrorStage = "execute"
					if prepared {
						s = fixture.Prepared(s)
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
			rrgScenario(t, func(c *rrgCase) {
				c.seed()
				c.exec("SET ROLE rrg_o", "REVOKE EXECUTE ON FUNCTION public.rrg_f() FROM PUBLIC", "GRANT EXECUTE ON FUNCTION public.rrg_f() TO rrg_r", "RESET ROLE")
				prior := [][]any{c.origin("rrg_o", "rrg_o", "YES"), c.origin("rrg_o", "rrg_r", "NO")}
				unchanged := func() {
					c.check(c.conn, "public", "rrg_f", prior...)
					c.run(c.conn, fixture.Select("SELECT public.rrg_f() AS result", []string{"result"}, []string{"INT4"}, [][]any{{int64(7)}}))
				}
				for _, tc := range []struct{ q, code, message string }{
					{"GRANT EXECUTE ON FUNCTION public.rrg_f() TO rrg_missing", "42704", `role "rrg_missing" does not exist`},
					{"REVOKE EXECUTE ON FUNCTION public.rrg_f() FROM rrg_missing", "42704", `role "rrg_missing" does not exist`},
					{"GRANT EXECUTE ON FUNCTION public.rrg_f() TO PUBLIC WITH GRANT OPTION", "0LP01", "grant options can only be granted to roles"},
					{"GRANT EXECUTE ON FUNCTION public.rrg_missing() TO rrg_r", "42883", "function public.rrg_missing() does not exist"},
					{"GRANT EXECUTE ON FUNCTION public.rrg_f() TO rrg_d,rrg_missing", "42704", `role "rrg_missing" does not exist`},
					{"REVOKE EXECUTE ON FUNCTION public.rrg_f() FROM rrg_r,rrg_missing", "42704", `role "rrg_missing" does not exist`},
					{"GRANT EXECUTE ON FUNCTION public.rrg_f(),public.rrg_missing() TO rrg_d", "42883", "function public.rrg_missing() does not exist"},
					{"REVOKE EXECUTE ON FUNCTION public.rrg_f(),public.rrg_missing() FROM rrg_r", "42883", "function public.rrg_missing() does not exist"},
				} {
					c.exec("SET ROLE rrg_o")
					s := fixture.ImplicitError(tc.q, fixture.Diagnostic{Code: tc.code, Message: tc.message})
					s.Prepared = prepared
					s.ErrorStage = "execute"
					c.run(c.conn, s)
					c.exec("RESET ROLE")
					unchanged()
				}
				c.actor("rrg_p", func() {
					s := fixture.ImplicitError("GRANT EXECUTE ON FUNCTION public.rrg_f() TO rrg_d", fixture.Diagnostic{Code: "42501", Message: "permission denied for function rrg_f"})
					s.Prepared = prepared
					s.ErrorStage = "execute"
					c.run(c.conn, s)
					c.check(c.conn, "public", "rrg_f")
				})
				unchanged()
				// A failed write in a transaction aborts only that transaction; a rollback
				// restores a usable held reader with the exact old ACL bag.
				held := c.hold(c.conn, rrgQuery)
				c.exec("BEGIN", "SET LOCAL ROLE rrg_o")
				s := fixture.ImplicitError("GRANT EXECUTE ON FUNCTION public.rrg_f() TO rrg_missing", fixture.Diagnostic{Code: "42704", Message: `role "rrg_missing" does not exist`})
				s.ErrorStage = "execute"
				s.Prepared = prepared
				c.run(c.conn, s)
				c.exec("ROLLBACK")
				c.check(held, "public", "rrg_f", prior...)
			})
		})
	}
}
