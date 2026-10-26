package reference_test

import (
	"stuntgres/reference/fixture"
	"testing"
	"unicode/utf8"
)

func TestReferenceInfoUserDefinedTypesDiagnostics(t *testing.T) {
	udtScenario(t, func(c *udtCase) {
		for _, prefix := range []string{"", "/* λ雪 */ "} {
			for _, prepared := range []bool{false, true} {
				s := fixture.ImplicitError(prefix+"SELECT missing FROM information_schema.user_defined_types", fixture.Diagnostic{Code: "42703", Message: `column "missing" does not exist`, Position: fixture.Position(utf8.RuneCountInString(prefix) + 8)})
				s.SelectRows, s.Prepared, s.ErrorStage = true, prepared, "execute"
				if prepared {
					s.ErrorStage = "prepare"
				}
				c.run(s)
			}
		}
		c.exec("CREATE SCHEMA udt_scope", "CREATE TYPE udt_scope.alpha AS (v integer)")
		where := "user_defined_type_schema='udt_scope'"
		want := udtRow(c.database, "udt_scope", "alpha")
		acl := func() string {
			var value string
			if err := c.c.QueryRowContext(c.p.Ctx, "SELECT COALESCE(typacl::text,'<NULL>') FROM pg_catalog.pg_type WHERE oid='udt_scope.alpha'::regtype").Scan(&value); err != nil {
				c.t.Fatal(err)
			}
			return value
		}
		before := acl()
		for _, mutation := range []struct{ q, code, message string }{
			{"GRANT USAGE ON TYPE udt_scope.alpha TO udt_missing", "42704", `role "udt_missing" does not exist`},
			{"GRANT USAGE ON TYPE udt_scope.alpha TO PUBLIC WITH GRANT OPTION", "0LP01", "grant options can only be granted to roles"},
		} {
			for _, prepared := range []bool{false, true} {
				s := fixture.ImplicitError(mutation.q, fixture.Diagnostic{Code: mutation.code, Message: mutation.message})
				s.Prepared, s.ErrorStage = prepared, "execute"
				c.run(s)
				c.check(where, want)
				if after := acl(); after != before {
					c.t.Fatalf("failed mutation changed ACL: %q -> %q", before, after)
				}
			}
		}
	})
}
