package reference_test

import (
	"database/sql"
	"testing"

	"stuntgres/reference/fixture"
)

func TestReferenceObjectGrantTypeTargets(t *testing.T) {
	objectGrantScenario(t, []string{"og_type_syntax"}, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn, run func(fixture.Step), prepared bool) {
		run(fixture.Exec("CREATE SCHEMA og_type_syntax", 0))
		run(fixture.Exec("GRANT USAGE,CREATE ON SCHEMA og_type_syntax TO og_object_reader", 0))
		run(fixture.Exec(`CREATE TYPE og_type_syntax."integer" AS ENUM('a')`, 0))
		run(fixture.Exec(`CREATE TYPE og_type_syntax."Mixed" AS ENUM('m')`, 0))
		run(fixture.Exec(`REVOKE USAGE ON TYPE og_type_syntax."integer",og_type_syntax."Mixed" FROM PUBLIC`, 0))
		run(fixture.Exec("SET search_path TO og_type_syntax", 0))
		defer func() {
			for _, q := range []string{"RESET SESSION AUTHORIZATION", "GRANT USAGE ON TYPE pg_catalog.int4 TO PUBLIC", "REVOKE USAGE ON TYPE pg_catalog.int4 FROM og_object_reader"} {
				if _, err := c.ExecContext(p.Ctx, q); err != nil {
					t.Error("builtin ACL cleanup", q, err)
				}
			}
		}()
		run(fixture.Exec("REVOKE USAGE ON TYPE pg_catalog.int4 FROM PUBLIC", 0))
		// Quoted integer resolves the actual enum name, not the builtin alias.
		run(fixture.Exec(`GRANT USAGE ON TYPE "integer" TO og_object_reader`, 0))
		fixture.RoleGrantActor(run, "og_object_reader", func() {
			run(fixture.Exec(`CREATE TABLE og_type_syntax.quoted_use(x "integer")`, 0))
			fixture.RoleGrantPermissionDenied(run, "CREATE TABLE og_type_syntax.builtin_denied(x INTEGER)", "type", "integer")
		})
		// GRANT uses a catalog name list, not declaration aliases: INTEGER
		// names the same enum here and does not authorize builtin int4.
		run(fixture.Exec("GRANT USAGE ON TYPE INTEGER TO og_object_reader", 0))
		fixture.RoleGrantActor(run, "og_object_reader", func() {
			fixture.RoleGrantPermissionDenied(run, "CREATE TABLE og_type_syntax.alias_still_denied(x INTEGER)", "type", "integer")
		})
		run(fixture.Exec("GRANT USAGE ON TYPE pg_catalog.int4 TO og_object_reader", 0))
		fixture.RoleGrantActor(run, "og_object_reader", func() { run(fixture.Exec("CREATE TABLE og_type_syntax.alias_use(x INTEGER)", 0)) })
		run(fixture.Exec(`GRANT USAGE ON TYPE og_type_syntax."Mixed" TO og_object_reader`, 0))
		fixture.RoleGrantActor(run, "og_object_reader", func() {
			run(fixture.Exec(`CREATE TABLE og_type_syntax.mixed_use(x og_type_syntax."Mixed")`, 0))
		})
		fixture.RoleGrantError(run, "GRANT USAGE ON TYPE og_type_syntax.mixed TO og_object_reader", "42704", `type "og_type_syntax.mixed" does not exist`)
		for _, target := range []string{"INTEGER[]", `og_type_syntax."Mixed"[]`} {
			fixture.RoleGrantSyntax(run, "GRANT USAGE ON TYPE "+target+" TO og_object_reader", "[", prepared)
		}
		run(fixture.ImplicitError("GRANT USAGE ON TYPE pg_catalog._int4 TO og_object_reader", fixture.Diagnostic{Code: "0LP01", Message: "cannot set privileges of array types", Hint: "Set the privileges of the element type instead."}))
		run(fixture.Exec(`GRANT USAGE ON TYPE "pg_catalog"."int4" TO og_object_reader`, 0))
		fixture.RoleGrantError(run, "GRANT USAGE ON DOMAIN pg_catalog.int4 TO og_object_reader", "42809", `"pg_catalog.int4" is not a domain`)
		run(fixture.Exec(`REVOKE USAGE ON TYPE pg_catalog.int4,"integer",og_type_syntax."Mixed" FROM og_object_reader`, 0))
		fixture.RoleGrantActor(run, "og_object_reader", func() {
			fixture.RoleGrantPermissionDenied(run, "CREATE TABLE og_type_syntax.alias_after_revoke(x INTEGER)", "type", "integer")
			fixture.RoleGrantPermissionDenied(run, `CREATE TABLE og_type_syntax.quoted_after_revoke(x "integer")`, "type", `"integer"`)
		})
	})
}
