package reference_test

import (
	"testing"
	"unicode/utf8"

	"stuntgres/reference/fixture"
)

func TestReferenceInfoRoleTableGrantsDiagnostics(t *testing.T) {
	t.Run("read", func(t *testing.T) {
		rtgScenario(t, func(c *rtgCase) {
			for _, prefix := range []string{"", "/* λ雪 */ "} {
				q := prefix + "SELECT missing FROM information_schema.role_table_grants"
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
			rtgScenario(t, func(c *rtgCase) {
				c.seed()
				c.run(c.conn, fixture.Exec("INSERT INTO rtg_t VALUES(7)", 1))
				c.exec("SET ROLE rtg_o", "GRANT SELECT ON rtg_t TO rtg_r", "RESET ROLE")
				prior := c.r("SELECT", "NO")
				unchanged := func() {
					c.check(c.conn, "rtg_t", "rtg_r", prior)
					c.check(c.conn, "rtg_t", "rtg_d")
					c.check(c.conn, "rtg_t", "PUBLIC")
					c.run(c.conn, fixture.Select("SELECT a FROM rtg_t", []string{"a"}, []string{"INT4"}, [][]any{{int64(7)}}))
				}
				for _, tc := range []struct{ q, code, message string }{
					{"GRANT UPDATE ON rtg_t TO rtg_missing_role", "42704", `role "rtg_missing_role" does not exist`},
					{"REVOKE SELECT ON rtg_t FROM rtg_missing_role", "42704", `role "rtg_missing_role" does not exist`},
					{"GRANT SELECT ON rtg_t TO PUBLIC WITH GRANT OPTION", "0LP01", "grant options can only be granted to roles"},
					{"GRANT UPDATE ON rtg_t TO rtg_r,rtg_missing_role", "42704", `role "rtg_missing_role" does not exist`},
					{"REVOKE SELECT ON rtg_t FROM rtg_r,rtg_missing_role", "42704", `role "rtg_missing_role" does not exist`},
					{"GRANT UPDATE ON rtg_t,rtg_missing_table TO rtg_r", "42P01", `relation "rtg_missing_table" does not exist`},
					{"REVOKE SELECT ON rtg_t,rtg_missing_table FROM rtg_r", "42P01", `relation "rtg_missing_table" does not exist`},
				} {
					c.exec("SET ROLE rtg_o")
					s := fixture.ImplicitError(tc.q, fixture.Diagnostic{Code: tc.code, Message: tc.message})
					s.Prepared = prepared
					s.ErrorStage = "execute"
					c.run(c.conn, s)
					c.exec("RESET ROLE")
					unchanged()
				}
				c.actor("rtg_p", func() {
					s := fixture.ImplicitError("GRANT UPDATE ON rtg_t TO rtg_d", fixture.Diagnostic{Code: "42501", Message: "permission denied for table rtg_t"})
					s.Prepared = prepared
					s.ErrorStage = "execute"
					c.run(c.conn, s)
					c.check(c.conn, "rtg_t", "rtg_d")
				})
				unchanged()
			})
		})
	}
}
