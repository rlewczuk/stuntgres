package reference_test

import (
	"stuntgres/reference/fixture"
	"testing"
	"unicode/utf8"
)

func TestReferenceInfoUsagePrivilegesDiagnostics(t *testing.T) {
	t.Run("read", func(t *testing.T) {
		upScenario(t, func(c *upCase) {
			for _, prefix := range []string{"", "/* λ雪 */ "} {
				q := prefix + "SELECT missing FROM information_schema.usage_privileges"
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
	t.Run("sequence_absent_atomicity", func(t *testing.T) {
		upScenario(t, func(c *upCase) {
			c.seed()
			c.exec("CREATE SEQUENCE public.up_s", "ALTER SEQUENCE public.up_s OWNER TO up_o", "CREATE TYPE public.up_composite AS(a integer)")
			held := c.hold(c.conn, upQuery)
			for _, prepared := range []bool{false, true} {
				for _, tc := range []struct{ q, code, message string }{
					{"GRANT USAGE ON SEQUENCE public.up_s TO up_r,up_missing", "42704", `role "up_missing" does not exist`},
					{"GRANT USAGE ON SEQUENCE public.up_s,public.up_missing TO up_r", "42P01", `relation "public.up_missing" does not exist`},
					{"GRANT USAGE ON DOMAIN public.up_composite TO up_r", "42809", `"public.up_composite" is not a domain`},
				} {
					c.exec("SET ROLE up_o")
					s := fixture.ImplicitError(tc.q, fixture.Diagnostic{Code: tc.code, Message: tc.message})
					s.Prepared, s.ErrorStage = prepared, "execute"
					c.run(c.conn, s)
					c.exec("RESET ROLE")
					c.sequenceACL("public.up_s", "absent")
					c.check(c.conn, "public", "up_s")
					c.check(held, "public", "up_s")
				}
				c.actor("up_p", func() {
					s := fixture.ImplicitError("GRANT USAGE ON SEQUENCE public.up_s TO up_r", fixture.Diagnostic{Code: "42501", Message: "permission denied for sequence up_s"})
					s.Prepared, s.ErrorStage = prepared, "execute"
					c.run(c.conn, s)
				})
				c.sequenceACL("public.up_s", "absent")
				c.check(held, "public", "up_s")
			}
		})
	})
	for _, prepared := range []bool{false, true} {
		name := "direct"
		if prepared {
			name = "prepared"
		}
		t.Run(name, func(t *testing.T) {
			upScenario(t, func(c *upCase) {
				c.seed()
				c.exec("SET ROLE up_o", "REVOKE USAGE ON TYPE public.up_t FROM PUBLIC", "GRANT USAGE ON TYPE public.up_t TO up_r", "RESET ROLE")
				prior := [][]any{c.origin("up_o", "up_o", "YES"), c.origin("up_o", "up_r", "NO")}
				held := c.hold(c.conn, upQuery)
				var acl string
				if err := c.conn.QueryRowContext(c.p.Ctx, "SELECT typacl::text FROM pg_type WHERE oid='public.up_t'::regtype").Scan(&acl); err != nil {
					c.t.Fatal(err)
				}
				unchanged := func() {
					c.check(c.conn, "public", "up_t", prior...)
					c.check(held, "public", "up_t", prior...)
					c.run(c.conn, fixture.Select("SELECT typacl::text AS acl FROM pg_type WHERE oid='public.up_t'::regtype", []string{"acl"}, []string{"TEXT"}, [][]any{{acl}}))
				}
				for _, tc := range []struct{ q, code, message string }{
					{"GRANT USAGE ON TYPE public.up_t TO up_missing", "42704", `role "up_missing" does not exist`},
					{"REVOKE USAGE ON TYPE public.up_t FROM up_missing", "42704", `role "up_missing" does not exist`},
					{"GRANT USAGE ON TYPE public.up_t TO PUBLIC WITH GRANT OPTION", "0LP01", "grant options can only be granted to roles"},
					{"GRANT USAGE ON TYPE public.up_missing TO up_r", "42704", `type "public.up_missing" does not exist`},
					{"GRANT USAGE ON TYPE public.up_t TO up_d,up_missing", "42704", `role "up_missing" does not exist`},
					{"REVOKE USAGE ON TYPE public.up_t FROM up_r,up_missing", "42704", `role "up_missing" does not exist`},
					{"GRANT USAGE ON TYPE public.up_t,public.up_missing TO up_d", "42704", `type "public.up_missing" does not exist`},
					{"REVOKE USAGE ON TYPE public.up_t,public.up_missing FROM up_r", "42704", `type "public.up_missing" does not exist`},
				} {
					c.exec("SET ROLE up_o")
					// Independently reasoned primary records; pinned Phase A resolves any
					// additional semantic fields/positions rather than learning native output.
					s := fixture.ImplicitError(tc.q, fixture.Diagnostic{Code: tc.code, Message: tc.message})
					s.Prepared = prepared
					s.ErrorStage = "execute"
					c.run(c.conn, s)
					c.exec("RESET ROLE")
					unchanged()
				}
				c.actor("up_p", func() {
					s := fixture.ImplicitError("GRANT USAGE ON TYPE public.up_t TO up_d", fixture.Diagnostic{Code: "42501", Message: "permission denied for type up_t"})
					s.Prepared = prepared
					s.ErrorStage = "execute"
					c.run(c.conn, s)
					c.check(c.conn, "public", "up_t")
				})
				unchanged()
				c.exec("BEGIN", "SET LOCAL ROLE up_o")
				s := fixture.ImplicitError("GRANT USAGE ON TYPE public.up_t TO up_missing", fixture.Diagnostic{Code: "42704", Message: `role "up_missing" does not exist`})
				s.Prepared = prepared
				s.ErrorStage = "execute"
				c.run(c.conn, s)
				c.exec("ROLLBACK")
				unchanged()
			})
		})
	}
}
