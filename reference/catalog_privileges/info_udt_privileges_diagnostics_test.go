package reference_test

import (
	"stuntgres/reference/fixture"
	"testing"
	"unicode/utf8"
)

func TestReferenceInfoUdtPrivilegesDiagnosticsAtomicity(t *testing.T) {
	t.Run("read", func(t *testing.T) {
		uvScenario(t, func(c *uvCase) {
			for _, prefix := range []string{"", "/* λ雪 */ "} {
				q := prefix + "SELECT missing FROM information_schema.udt_privileges"
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
			uvScenario(t, func(c *uvCase) {
				c.seed()
				c.exec("SET ROLE uv_o", "REVOKE USAGE ON TYPE public.uv_t FROM PUBLIC", "GRANT USAGE ON TYPE public.uv_t TO uv_r", "RESET ROLE")
				prior := [][]any{c.origin("uv_o", "uv_o", "YES"), c.origin("uv_o", "uv_r", "NO")}
				held := c.hold(c.conn, uvQuery)
				var acl string
				if err := c.conn.QueryRowContext(c.p.Ctx, "SELECT typacl::text FROM pg_type WHERE oid='public.uv_t'::regtype").Scan(&acl); err != nil {
					c.t.Fatal(err)
				}
				unchanged := func() {
					c.check(c.conn, "public", "uv_t", prior...)
					c.check(held, "public", "uv_t", prior...)
					c.run(c.conn, fixture.Select("SELECT typacl::text AS acl FROM pg_type WHERE oid='public.uv_t'::regtype", []string{"acl"}, []string{"TEXT"}, [][]any{{acl}}))
				}
				for _, tc := range []struct{ q, code, message string }{
					{"GRANT USAGE ON TYPE public.uv_t TO uv_missing", "42704", `role "uv_missing" does not exist`},
					{"REVOKE USAGE ON TYPE public.uv_t FROM uv_missing", "42704", `role "uv_missing" does not exist`},
					{"GRANT USAGE ON TYPE public.uv_t TO PUBLIC WITH GRANT OPTION", "0LP01", "grant options can only be granted to roles"},
					{"GRANT USAGE ON TYPE public.uv_missing TO uv_r", "42704", `type "public.uv_missing" does not exist`},
					{"GRANT USAGE ON TYPE public.uv_t TO uv_d,uv_missing", "42704", `role "uv_missing" does not exist`},
					{"REVOKE USAGE ON TYPE public.uv_t FROM uv_r,uv_missing", "42704", `role "uv_missing" does not exist`},
					{"GRANT USAGE ON TYPE public.uv_t,public.uv_missing TO uv_d", "42704", `type "public.uv_missing" does not exist`},
					{"REVOKE USAGE ON TYPE public.uv_t,public.uv_missing FROM uv_r", "42704", `type "public.uv_missing" does not exist`},
				} {
					c.exec("SET ROLE uv_o")
					// Complete diagnostic records are independently specified and
					// confirmed against the pinned PostgreSQL 18.6 oracle.
					s := fixture.ImplicitError(tc.q, fixture.Diagnostic{Code: tc.code, Message: tc.message})
					s.Prepared = prepared
					s.ErrorStage = "execute"
					c.run(c.conn, s)
					c.exec("RESET ROLE")
					unchanged()
				}
				c.actor("uv_p", func() {
					s := fixture.ImplicitError("GRANT USAGE ON TYPE public.uv_t TO uv_d", fixture.Diagnostic{Code: "42501", Message: "permission denied for type uv_t"})
					s.Prepared = prepared
					s.ErrorStage = "execute"
					c.run(c.conn, s)
					c.check(c.conn, "public", "uv_t")
				})
				unchanged()
				c.exec("BEGIN", "SET LOCAL ROLE uv_o")
				s := fixture.ImplicitError("GRANT USAGE ON TYPE public.uv_t TO uv_missing", fixture.Diagnostic{Code: "42704", Message: `role "uv_missing" does not exist`})
				s.Prepared = prepared
				s.ErrorStage = "execute"
				c.run(c.conn, s)
				c.exec("ROLLBACK")
				unchanged()
			})
		})
	}
}
