package reference_test

import (
	"stuntgres/reference/fixture"
	"testing"
	"unicode/utf8"
)

func TestReferenceInfoRoleUsageGrantsDiagnostics(t *testing.T) {
	t.Run("read", func(t *testing.T) {
		rugScenario(t, func(c *rugCase) {
			for _, prefix := range []string{"", "/* λ雪 */ "} {
				q := prefix + "SELECT missing FROM information_schema.role_usage_grants"
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
		rugScenario(t, func(c *rugCase) {
			c.seed()
			c.exec("CREATE SEQUENCE public.rug_s", "ALTER SEQUENCE public.rug_s OWNER TO rug_o", "CREATE TYPE public.rug_composite AS(a integer)")
			held := c.hold(c.conn, rugQuery)
			for _, prepared := range []bool{false, true} {
				for _, tc := range []struct{ q, code, message string }{
					{"GRANT USAGE ON SEQUENCE public.rug_s TO rug_r,rug_missing", "42704", `role "rug_missing" does not exist`},
					{"GRANT USAGE ON SEQUENCE public.rug_s,public.rug_missing TO rug_r", "42P01", `relation "public.rug_missing" does not exist`},
					{"GRANT USAGE ON DOMAIN public.rug_composite TO rug_r", "42809", `"public.rug_composite" is not a domain`},
				} {
					c.exec("SET ROLE rug_o")
					s := fixture.ImplicitError(tc.q, fixture.Diagnostic{Code: tc.code, Message: tc.message})
					s.Prepared, s.ErrorStage = prepared, "execute"
					c.run(c.conn, s)
					c.exec("RESET ROLE")
					c.sequenceACL("public.rug_s", "absent")
					c.check(c.conn, "public", "rug_s")
					c.check(held, "public", "rug_s")
				}
				c.actor("rug_p", func() {
					s := fixture.ImplicitError("GRANT USAGE ON SEQUENCE public.rug_s TO rug_r", fixture.Diagnostic{Code: "42501", Message: "permission denied for sequence rug_s"})
					s.Prepared, s.ErrorStage = prepared, "execute"
					c.run(c.conn, s)
				})
				c.sequenceACL("public.rug_s", "absent")
				c.check(held, "public", "rug_s")
			}
		})
	})
	for _, prepared := range []bool{false, true} {
		name := "direct"
		if prepared {
			name = "prepared"
		}
		t.Run(name, func(t *testing.T) {
			rugScenario(t, func(c *rugCase) {
				c.seed()
				c.exec("SET ROLE rug_o", "REVOKE USAGE ON TYPE public.rug_t FROM PUBLIC", "GRANT USAGE ON TYPE public.rug_t TO rug_r", "RESET ROLE")
				prior := [][]any{c.origin("rug_o", "rug_o", "YES"), c.origin("rug_o", "rug_r", "NO")}
				held := c.hold(c.conn, rugQuery)
				var acl string
				if err := c.conn.QueryRowContext(c.p.Ctx, "SELECT typacl::text FROM pg_type WHERE oid='public.rug_t'::regtype").Scan(&acl); err != nil {
					c.t.Fatal(err)
				}
				unchanged := func() {
					c.check(c.conn, "public", "rug_t", prior...)
					c.check(held, "public", "rug_t", prior...)
					c.run(c.conn, fixture.Select("SELECT typacl::text AS acl FROM pg_type WHERE oid='public.rug_t'::regtype", []string{"acl"}, []string{"TEXT"}, [][]any{{acl}}))
				}
				for _, tc := range []struct{ q, code, message string }{
					{"GRANT USAGE ON TYPE public.rug_t TO rug_missing", "42704", `role "rug_missing" does not exist`},
					{"REVOKE USAGE ON TYPE public.rug_t FROM rug_missing", "42704", `role "rug_missing" does not exist`},
					{"GRANT USAGE ON TYPE public.rug_t TO PUBLIC WITH GRANT OPTION", "0LP01", "grant options can only be granted to roles"},
					{"GRANT USAGE ON TYPE public.rug_missing TO rug_r", "42704", `type "public.rug_missing" does not exist`},
					{"GRANT USAGE ON TYPE public.rug_t TO rug_d,rug_missing", "42704", `role "rug_missing" does not exist`},
					{"REVOKE USAGE ON TYPE public.rug_t FROM rug_r,rug_missing", "42704", `role "rug_missing" does not exist`},
					{"GRANT USAGE ON TYPE public.rug_t,public.rug_missing TO rug_d", "42704", `type "public.rug_missing" does not exist`},
					{"REVOKE USAGE ON TYPE public.rug_t,public.rug_missing FROM rug_r", "42704", `type "public.rug_missing" does not exist`},
				} {
					c.exec("SET ROLE rug_o")
					// Independently reasoned primary records; pinned Phase A resolves any
					// additional semantic fields/positions rather than learning native output.
					s := fixture.ImplicitError(tc.q, fixture.Diagnostic{Code: tc.code, Message: tc.message})
					s.Prepared = prepared
					s.ErrorStage = "execute"
					c.run(c.conn, s)
					c.exec("RESET ROLE")
					unchanged()
				}
				c.actor("rug_p", func() {
					s := fixture.ImplicitError("GRANT USAGE ON TYPE public.rug_t TO rug_d", fixture.Diagnostic{Code: "42501", Message: "permission denied for type rug_t"})
					s.Prepared = prepared
					s.ErrorStage = "execute"
					c.run(c.conn, s)
					c.check(c.conn, "public", "rug_t")
				})
				unchanged()
				c.exec("BEGIN", "SET LOCAL ROLE rug_o")
				s := fixture.ImplicitError("GRANT USAGE ON TYPE public.rug_t TO rug_missing", fixture.Diagnostic{Code: "42704", Message: `role "rug_missing" does not exist`})
				s.Prepared = prepared
				s.ErrorStage = "execute"
				c.run(c.conn, s)
				c.exec("ROLLBACK")
				unchanged()
			})
		})
	}
}
