package reference_test

import (
	"stuntgres/reference/fixture"
	"testing"
	"unicode/utf8"
)

func TestReferenceInfoRoleUdtGrantsDiagnosticsAtomicity(t *testing.T) {
	t.Run("read", func(t *testing.T) {
		ugScenario(t, func(c *ugCase) {
			for _, prefix := range []string{"", "/* λ雪 */ "} {
				q := prefix + "SELECT missing FROM information_schema.role_udt_grants"
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
			ugScenario(t, func(c *ugCase) {
				c.seed()
				c.exec("SET ROLE ug_o", "REVOKE USAGE ON TYPE public.ug_t FROM PUBLIC", "GRANT USAGE ON TYPE public.ug_t TO ug_r", "RESET ROLE")
				prior := [][]any{c.origin("ug_o", "ug_o", "YES"), c.origin("ug_o", "ug_r", "NO")}
				held := c.hold(c.conn, ugQuery)
				var acl string
				if err := c.conn.QueryRowContext(c.p.Ctx, "SELECT typacl::text FROM pg_type WHERE oid='public.ug_t'::regtype").Scan(&acl); err != nil {
					c.t.Fatal(err)
				}
				unchanged := func() {
					c.check(c.conn, "public", "ug_t", prior...)
					c.check(held, "public", "ug_t", prior...)
					c.run(c.conn, fixture.Select("SELECT typacl::text AS acl FROM pg_type WHERE oid='public.ug_t'::regtype", []string{"acl"}, []string{"TEXT"}, [][]any{{acl}}))
				}
				for _, tc := range []struct{ q, code, message string }{
					{"GRANT USAGE ON TYPE public.ug_t TO ug_missing", "42704", `role "ug_missing" does not exist`},
					{"REVOKE USAGE ON TYPE public.ug_t FROM ug_missing", "42704", `role "ug_missing" does not exist`},
					{"GRANT USAGE ON TYPE public.ug_t TO PUBLIC WITH GRANT OPTION", "0LP01", "grant options can only be granted to roles"},
					{"GRANT USAGE ON TYPE public.ug_missing TO ug_r", "42704", `type "public.ug_missing" does not exist`},
					{"GRANT USAGE ON TYPE public.ug_t TO ug_d,ug_missing", "42704", `role "ug_missing" does not exist`},
					{"REVOKE USAGE ON TYPE public.ug_t FROM ug_r,ug_missing", "42704", `role "ug_missing" does not exist`},
					{"GRANT USAGE ON TYPE public.ug_t,public.ug_missing TO ug_d", "42704", `type "public.ug_missing" does not exist`},
					{"REVOKE USAGE ON TYPE public.ug_t,public.ug_missing FROM ug_r", "42704", `type "public.ug_missing" does not exist`},
				} {
					c.exec("SET ROLE ug_o")
					// Independently reasoned primary records; pinned Phase A resolves any
					// additional semantic fields/positions rather than learning native output.
					s := fixture.ImplicitError(tc.q, fixture.Diagnostic{Code: tc.code, Message: tc.message})
					s.Prepared = prepared
					s.ErrorStage = "execute"
					c.run(c.conn, s)
					c.exec("RESET ROLE")
					unchanged()
				}
				c.actor("ug_p", func() {
					s := fixture.ImplicitError("GRANT USAGE ON TYPE public.ug_t TO ug_d", fixture.Diagnostic{Code: "42501", Message: "permission denied for type ug_t"})
					s.Prepared = prepared
					s.ErrorStage = "execute"
					c.run(c.conn, s)
					c.check(c.conn, "public", "ug_t")
				})
				unchanged()
				c.exec("BEGIN", "SET LOCAL ROLE ug_o")
				s := fixture.ImplicitError("GRANT USAGE ON TYPE public.ug_t TO ug_missing", fixture.Diagnostic{Code: "42704", Message: `role "ug_missing" does not exist`})
				s.Prepared = prepared
				s.ErrorStage = "execute"
				c.run(c.conn, s)
				c.exec("ROLLBACK")
				unchanged()
			})
		})
	}
}
