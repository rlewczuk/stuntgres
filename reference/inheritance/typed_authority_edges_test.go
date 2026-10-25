package reference_test

import (
	"context"
	"database/sql"
	"stuntgres/reference/fixture"
	"testing"
)

func TestReferenceTypedTableSchemaAndMemberUsage(t *testing.T) {
	typedTableScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn, prepared bool, run func(fixture.Step)) {
		role := "typed_member_actor"
		fixture.RoleReferenceCleanup(t, []string{role})
		defer c.ExecContext(context.Background(), "RESET SESSION AUTHORIZATION")
		run(fixture.Exec("CREATE USER "+role, 0))
		run(fixture.Exec("CREATE SCHEMA typed_private", 0))
		defer b.DB.ExecContext(context.Background(), "DROP SCHEMA IF EXISTS typed_private CASCADE")
		run(fixture.Exec("CREATE TYPE pair AS (id integer)", 0))
		run(fixture.Exec("SET SESSION AUTHORIZATION "+role, 0))
		d := typedTableError("42501", "permission denied for schema typed_private")
		d.Position = "14"
		fixture.CompositeReferenceError(t, p, c, "CREATE TABLE typed_private.denied OF pair", "execute", d)
		run(fixture.Exec("RESET SESSION AUTHORIZATION", 0))
		if b.PG {
			run(fixture.Exec("GRANT USAGE,CREATE ON SCHEMA public TO "+role, 0))
			run(fixture.Exec("CREATE TYPE member AS (v integer)", 0))
			run(fixture.Exec("REVOKE USAGE ON TYPE member FROM PUBLIC", 0))
			run(fixture.Exec("ALTER TYPE pair OWNER TO "+role, 0))
			run(fixture.Exec("SET SESSION AUTHORIZATION "+role, 0))
			fixture.CompositeReferenceError(t, p, c, "ALTER TYPE pair ADD ATTRIBUTE v member", "execute", typedTableError("42501", "permission denied for type member"))
			fixture.CompositeReferenceError(t, p, c, "DROP TYPE pair,member CASCADE", "execute", typedTableError("42501", "must be owner of type member"))
			run(fixture.Exec("CREATE TABLE probe OF pair", 0))
			run(fixture.Select("SELECT * FROM probe", []string{"id"}, []string{"INT4"}, nil))
			run(fixture.Exec("RESET SESSION AUTHORIZATION", 0))
			run(fixture.Exec("GRANT USAGE ON TYPE member TO "+role, 0))
			run(fixture.Exec("SET SESSION AUTHORIZATION "+role, 0))
			run(fixture.Exec("ALTER TYPE pair ADD ATTRIBUTE v member CASCADE", 0))
			run(fixture.Exec("DROP TYPE pair CASCADE", 0))
			run(fixture.Exec("RESET SESSION AUTHORIZATION", 0))
			run(fixture.Exec("DROP TYPE member", 0))
			run(fixture.Exec("REVOKE USAGE,CREATE ON SCHEMA public FROM "+role, 0))
		}
		run(fixture.Exec("DROP SCHEMA typed_private", 0))
		run(fixture.Exec("DROP ROLE "+role, 0))
	})
}
