package reference_test

import (
	"testing"
	"unicode/utf8"

	"stuntgres/reference/fixture"
)

func TestReferenceInfoRoleColumnGrantsDiagnostics(t *testing.T) {
	t.Run("read", func(t *testing.T) {
		rcgScenario(t, func(c *rcgCase) {
			for _, prefix := range []string{"", "/* λ雪 */ "} {
				q := prefix + "SELECT missing FROM information_schema.role_column_grants"
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
			rcgScenario(t, func(c *rcgCase) {
				c.seed()
				c.exec("CREATE TABLE rcg_u(b integer)", "ALTER TABLE rcg_u OWNER TO rcg_o", "SET ROLE rcg_o", "GRANT SELECT(a) ON rcg_t TO rcg_r", "GRANT UPDATE(b) ON rcg_u TO rcg_r", "RESET ROLE")
				prior := c.r("a", "SELECT", "NO")
				other := c.row("rcg_o", "rcg_r", "public", "rcg_u", "b", "UPDATE", "NO")
				for _, tc := range []struct{ q, code, message string }{
					{"GRANT SELECT(absent) ON rcg_t TO rcg_r", "42703", `column "absent" of relation "rcg_t" does not exist`},
					{"REVOKE SELECT(absent) ON rcg_t FROM rcg_r", "42703", `column "absent" of relation "rcg_t" does not exist`},
					{"GRANT SELECT(a) ON rcg_t TO rcg_missing_role", "42704", `role "rcg_missing_role" does not exist`},
					{"REVOKE SELECT(a) ON rcg_t FROM rcg_missing_role", "42704", `role "rcg_missing_role" does not exist`},
					{"GRANT DELETE(a) ON rcg_t TO rcg_r", "0LP01", "invalid privilege type DELETE for column"},
					{"REVOKE DELETE(a) ON rcg_t FROM rcg_r", "0LP01", "invalid privilege type DELETE for column"},
					{"GRANT SELECT(a) ON rcg_t TO PUBLIC WITH GRANT OPTION", "0LP01", "grant options can only be granted to roles"},
					{"GRANT SELECT(a) ON rcg_t,rcg_u TO rcg_r", "42703", `column "a" of relation "rcg_u" does not exist`},
					{"REVOKE SELECT(a) ON rcg_t,rcg_u FROM rcg_r", "42703", `column "a" of relation "rcg_u" does not exist`},
					{"GRANT UPDATE ON rcg_t TO rcg_r,rcg_missing_role", "42704", `role "rcg_missing_role" does not exist`},
					{"REVOKE SELECT ON rcg_t FROM rcg_r,rcg_missing_role", "42704", `role "rcg_missing_role" does not exist`},
				} {
					c.exec("SET ROLE rcg_o")
					s := fixture.ImplicitError(tc.q, fixture.Diagnostic{Code: tc.code, Message: tc.message})
					s.ErrorStage = "execute"
					s.Prepared = prepared
					c.run(c.conn, s)
					c.exec("RESET ROLE")
					c.check(c.conn, "rcg_t", "rcg_r", prior)
					c.check(c.conn, "rcg_u", "rcg_r", other)
				}
				c.actor("rcg_p", func() {
					s := fixture.ImplicitError("GRANT SELECT(a) ON rcg_t TO rcg_d", fixture.Diagnostic{Code: "42501", Message: `permission denied for column "a" of relation "rcg_t"`})
					s.ErrorStage = "execute"
					s.Prepared = prepared
					c.run(c.conn, s)
					c.check(c.conn, "rcg_t", "rcg_d")
				})
				c.check(c.conn, "rcg_t", "rcg_r", prior)
				c.check(c.conn, "rcg_t", "rcg_d")
			})
		})
	}
}
