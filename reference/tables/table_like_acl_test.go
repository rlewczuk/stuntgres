package reference_test

import (
	"context"
	"database/sql"
	"fmt"
	"stuntgres/reference/fixture"
	"testing"
)

func TestReferenceTableLikeSourceACL(t *testing.T) {
	fixture.TableLikeScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn, prepared bool, run func(fixture.Step)) {
		role := fmt.Sprintf("like_reader_%t", prepared)
		fixture.RoleReferenceCleanup(t, []string{role})
		defer c.ExecContext(context.Background(), "RESET SESSION AUTHORIZATION")
		run(fixture.Exec("CREATE USER "+role, 0))
		if b.PG {
			run(fixture.Exec("GRANT USAGE,CREATE ON SCHEMA public TO "+role, 0))
		}
		run(fixture.Exec("CREATE TABLE src(a INT NOT NULL DEFAULT 7)", 0))
		run(fixture.Exec("CREATE SEQUENCE seq", 0))
		run(fixture.Exec("CREATE INDEX idx ON src(a)", 0))
		run(fixture.Exec("CREATE SCHEMA like_private", 0))
		defer b.DB.ExecContext(context.Background(), "DROP SCHEMA IF EXISTS like_private CASCADE")
		run(fixture.Exec("CREATE TABLE like_private.src(a INT)", 0))
		run(fixture.Exec("SET SESSION AUTHORIZATION "+role, 0))
		run(fixture.TableLikeError("CREATE TABLE clone (LIKE src INCLUDING ALL)", fixture.Diagnostic{Code: "42501", Message: "permission denied for table src"}))
		run(fixture.TableLikeError("CREATE TABLE clone (LIKE seq INCLUDING ALL)", fixture.Diagnostic{Code: "42809", Message: `relation "seq" is invalid in LIKE clause`, Detail: "This operation is not supported for sequences.", Position: "26"}))
		run(fixture.TableLikeError("CREATE TABLE clone (LIKE idx INCLUDING ALL)", fixture.Diagnostic{Code: "42809", Message: `relation "idx" is invalid in LIKE clause`, Detail: "This operation is not supported for indexes.", Position: "26"}))
		run(fixture.TableLikeError("CREATE TABLE clone (LIKE like_private.src INCLUDING ALL)", fixture.Diagnostic{Code: "42501", Message: "permission denied for schema like_private", Position: "26"}))
		run(fixture.Exec("RESET SESSION AUTHORIZATION", 0))
		run(fixture.Exec("GRANT SELECT(a) ON src TO "+role, 0))
		run(fixture.Exec("SET SESSION AUTHORIZATION "+role, 0))
		run(fixture.TableLikeError("CREATE TABLE clone (LIKE src INCLUDING ALL)", fixture.Diagnostic{Code: "42501", Message: "permission denied for table src"}))
		run(fixture.Exec("RESET SESSION AUTHORIZATION", 0))
		run(fixture.Exec("GRANT SELECT ON src TO "+role, 0))
		run(fixture.Exec("SET SESSION AUTHORIZATION "+role, 0))
		run(fixture.Exec("CREATE TABLE clone(LIKE src INCLUDING ALL)", 0))
		run(fixture.Exec("INSERT INTO clone DEFAULT VALUES", 1))
		run(fixture.TableLikeError("DROP TABLE clone,src CASCADE", fixture.Diagnostic{Code: "42501", Message: "must be owner of table src"}))
		run(fixture.Select("SELECT * FROM clone", []string{"a"}, []string{"INT4"}, [][]any{{int64(7)}}))
		run(fixture.Exec("RESET SESSION AUTHORIZATION", 0))
		if b.PG {
			run(fixture.Select("SELECT pg_get_userbyid(relowner) AS owner,relacl IS NULL AS default_acl FROM pg_class WHERE oid='clone'::regclass", []string{"owner", "default_acl"}, []string{"NAME", "BOOL"}, [][]any{{role, true}}))
		}
		run(fixture.Exec("DROP TABLE src CASCADE", 0))
		run(fixture.Select("SELECT * FROM clone", []string{"a"}, []string{"INT4"}, [][]any{{int64(7)}}))
		if b.PG {
			run(fixture.Exec("REVOKE USAGE,CREATE ON SCHEMA public FROM "+role, 0))
		}
		run(fixture.TableLikeError("DROP ROLE "+role, fixture.Diagnostic{Code: "2BP01", Message: `role "` + role + `" cannot be dropped because some objects depend on it`, Detail: "owner of table clone"}))
		run(fixture.Exec("DROP TABLE clone", 0))
		run(fixture.Exec("DROP SCHEMA like_private CASCADE", 0))
		run(fixture.Exec("DROP ROLE "+role, 0))
	})
}
