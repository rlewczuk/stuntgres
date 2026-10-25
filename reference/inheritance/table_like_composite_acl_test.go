package reference_test

import (
	"context"
	"database/sql"
	"fmt"
	"stuntgres/reference/fixture"
	"testing"
)

func TestReferenceTableLikeCompositeSourceACL(t *testing.T) {
	fixture.TableLikeScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn, prepared bool, run func(fixture.Step)) {
		// GRANT/REVOKE is the existing PostgreSQL-only authority setup; native
		// type-versus-backing-relation ACLs use the engine descriptor seam.
		if !b.PG {
			return
		}
		role := fmt.Sprintf("composite_like_reader_%t", prepared)
		fixture.RoleReferenceCleanup(t, []string{role})
		defer c.ExecContext(context.Background(), "RESET SESSION AUTHORIZATION")
		run(fixture.Exec("CREATE USER "+role, 0))
		run(fixture.Exec("GRANT USAGE,CREATE ON SCHEMA public TO "+role, 0))
		run(fixture.Exec("CREATE TYPE source_type AS (a INT,b TEXT)", 0))
		run(fixture.Exec("REVOKE USAGE ON TYPE source_type FROM PUBLIC", 0))
		run(fixture.Exec("CREATE SCHEMA like_composite_private", 0))
		defer b.DB.ExecContext(context.Background(), "DROP SCHEMA IF EXISTS like_composite_private CASCADE")
		run(fixture.Exec("CREATE TYPE like_composite_private.source_type AS (a INT)", 0))
		run(fixture.Exec("SET SESSION AUTHORIZATION "+role, 0))
		run(fixture.TableLikeError("CREATE TABLE clone (LIKE source_type INCLUDING ALL)", fixture.Diagnostic{Code: "42501", Message: "permission denied for type source_type"}))
		run(fixture.TableLikeError("CREATE TABLE clone (LIKE like_composite_private.source_type INCLUDING ALL)", fixture.Diagnostic{Code: "42501", Message: "permission denied for schema like_composite_private", Position: "26"}))
		run(fixture.Exec("RESET SESSION AUTHORIZATION", 0))
		run(fixture.Exec("GRANT USAGE ON TYPE source_type TO "+role, 0))
		// No SELECT privilege on the backing relation is granted. Type USAGE alone
		// authorizes the source; destination privileges belong to its new owner.
		run(fixture.Exec("SET SESSION AUTHORIZATION "+role, 0))
		run(fixture.Exec("CREATE TABLE clone(LIKE source_type INCLUDING ALL)", 0))
		run(fixture.Exec("INSERT INTO clone VALUES(7,'reader')", 1))
		run(fixture.Select("SELECT * FROM clone", []string{"a", "b"}, []string{"INT4", "TEXT"}, [][]any{{int64(7), "reader"}}))
		run(fixture.Exec("RESET SESSION AUTHORIZATION", 0))
		run(fixture.Select("SELECT pg_get_userbyid(relowner) AS owner,relacl IS NULL AS default_acl FROM pg_class WHERE oid='clone'::regclass", []string{"owner", "default_acl"}, []string{"NAME", "BOOL"}, [][]any{{role, true}}))
		run(fixture.Exec("DROP TYPE source_type", 0))
		run(fixture.Exec("INSERT INTO clone VALUES(NULL,NULL)", 1))
		run(fixture.Exec("DROP TABLE clone", 0))
		run(fixture.Exec("DROP SCHEMA like_composite_private CASCADE", 0))
		run(fixture.Exec("REVOKE USAGE,CREATE ON SCHEMA public FROM "+role, 0))
		run(fixture.Exec("DROP ROLE "+role, 0))
	})
}
