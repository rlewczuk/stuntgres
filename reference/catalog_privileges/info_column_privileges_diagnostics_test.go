package reference_test

import (
	"testing"
	"unicode/utf8"

	"stuntgres/reference/fixture"
)

func TestReferenceInfoColumnPrivilegesDiagnostics(t *testing.T) {
	t.Run("missing_projection", func(t *testing.T) {
		cpScenario(t, func(c *cpCase) {
			for _, prefix := range []string{"", "/* λ雪 */ "} {
				for _, prepared := range []bool{false, true} {
					s := fixture.ImplicitError(prefix+"SELECT missing FROM information_schema.column_privileges", fixture.Diagnostic{Code: "42703", Message: `column "missing" does not exist`, Position: fixture.Position(utf8.RuneCountInString(prefix) + 8)})
					s.SelectRows, s.Prepared, s.ErrorStage = true, prepared, "execute"
					if prepared {
						s.ErrorStage = "prepare"
					}
					c.run(c.conn, s)
					c.check(c.conn, "cp_missing", c.owner)
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
			cpScenario(t, func(c *cpCase) {
				c.seed()
				c.exec("CREATE TABLE cp_u(b integer)", "ALTER TABLE cp_u OWNER TO cp_o", "SET ROLE cp_o", "GRANT SELECT(a) ON cp_t TO cp_r", "GRANT UPDATE(b) ON cp_u TO cp_r", "RESET ROLE")
				prior := c.r("a", "SELECT", "NO")
				other := c.row("cp_o", "cp_r", "public", "cp_u", "b", "UPDATE", "NO")
				before := append(c.baseline("cp_o", "cp_t", "a", "b"), c.baseline("cp_o", "cp_u", "b")...)
				before = append(before, prior, other)
				unchanged := func() {
					s := c.step("SELECT * FROM information_schema.column_privileges WHERE table_schema='public' AND table_name IN ('cp_t','cp_u')", before)
					c.run(c.conn, s)
					c.run(c.conn, fixture.Prepared(s))
				}
				for _, tc := range []struct{ query, code, message string }{
					{"GRANT SELECT(absent) ON cp_t TO cp_r", "42703", `column "absent" of relation "cp_t" does not exist`},
					{"REVOKE SELECT(absent) ON cp_t FROM cp_r", "42703", `column "absent" of relation "cp_t" does not exist`},
					{"GRANT SELECT(a) ON cp_t TO cp_missing_role", "42704", `role "cp_missing_role" does not exist`},
					{"REVOKE SELECT(a) ON cp_t FROM cp_missing_role", "42704", `role "cp_missing_role" does not exist`},
					{"GRANT DELETE(a) ON cp_t TO cp_r", "0LP01", "invalid privilege type DELETE for column"},
					{"REVOKE DELETE(a) ON cp_t FROM cp_r", "0LP01", "invalid privilege type DELETE for column"},
					{"GRANT SELECT(a) ON cp_t TO PUBLIC WITH GRANT OPTION", "0LP01", "grant options can only be granted to roles"},
					{"GRANT SELECT(a) ON cp_t,cp_u TO cp_r", "42703", `column "a" of relation "cp_u" does not exist`},
					{"REVOKE SELECT(a) ON cp_t,cp_u FROM cp_r", "42703", `column "a" of relation "cp_u" does not exist`},
					{"GRANT UPDATE ON cp_t TO cp_r,cp_missing_role", "42704", `role "cp_missing_role" does not exist`},
					{"REVOKE SELECT ON cp_t FROM cp_r,cp_missing_role", "42704", `role "cp_missing_role" does not exist`},
				} {
					c.exec("SET ROLE cp_o")
					s := fixture.ImplicitError(tc.query, fixture.Diagnostic{Code: tc.code, Message: tc.message})
					s.Prepared, s.ErrorStage = prepared, "execute"
					c.run(c.conn, s)
					c.exec("RESET ROLE")
					unchanged()
				}
				c.actor("cp_p", func() {
					s := fixture.ImplicitError("GRANT SELECT(a) ON cp_t TO cp_d", fixture.Diagnostic{Code: "42501", Message: `permission denied for column "a" of relation "cp_t"`})
					s.Prepared, s.ErrorStage = prepared, "execute"
					c.run(c.conn, s)
					c.check(c.conn, "cp_t", "cp_d")
				})
				unchanged()
			})
		})
	}
}
