package reference_test

import (
	"context"
	"database/sql"
	"fmt"
	"testing"
)

func TestReferenceTableLikeSourceACL(t *testing.T) {
	tableLikeScenario(t, func(t *testing.T, p *referencePair, b referenceBackend, c *sql.Conn, prepared bool, run func(referenceStep)) {
		role := fmt.Sprintf("like_reader_%t", prepared)
		roleReferenceCleanup(t, []string{role})
		defer c.ExecContext(context.Background(), "RESET SESSION AUTHORIZATION")
		run(referenceExec("CREATE USER "+role, 0))
		if b.pg {
			run(referenceExec("GRANT USAGE,CREATE ON SCHEMA public TO "+role, 0))
		}
		run(referenceExec("CREATE TABLE src(a INT NOT NULL DEFAULT 7)", 0))
		run(referenceExec("CREATE SEQUENCE seq", 0))
		run(referenceExec("CREATE INDEX idx ON src(a)", 0))
		run(referenceExec("CREATE SCHEMA like_private", 0))
		defer b.db.ExecContext(context.Background(), "DROP SCHEMA IF EXISTS like_private CASCADE")
		run(referenceExec("CREATE TABLE like_private.src(a INT)", 0))
		run(referenceExec("SET SESSION AUTHORIZATION "+role, 0))
		run(tableLikeError("CREATE TABLE clone (LIKE src INCLUDING ALL)", referenceDiagnostic{Code: "42501", Message: "permission denied for table src"}))
		run(tableLikeError("CREATE TABLE clone (LIKE seq INCLUDING ALL)", referenceDiagnostic{Code: "42809", Message: `relation "seq" is invalid in LIKE clause`, Detail: "This operation is not supported for sequences.", Position: "26"}))
		run(tableLikeError("CREATE TABLE clone (LIKE idx INCLUDING ALL)", referenceDiagnostic{Code: "42809", Message: `relation "idx" is invalid in LIKE clause`, Detail: "This operation is not supported for indexes.", Position: "26"}))
		run(tableLikeError("CREATE TABLE clone (LIKE like_private.src INCLUDING ALL)", referenceDiagnostic{Code: "42501", Message: "permission denied for schema like_private", Position: "26"}))
		run(referenceExec("RESET SESSION AUTHORIZATION", 0))
			run(referenceExec("GRANT SELECT(a) ON src TO "+role, 0))
			run(referenceExec("SET SESSION AUTHORIZATION "+role, 0))
			run(tableLikeError("CREATE TABLE clone (LIKE src INCLUDING ALL)", referenceDiagnostic{Code: "42501", Message: "permission denied for table src"}))
			run(referenceExec("RESET SESSION AUTHORIZATION", 0))
			run(referenceExec("GRANT SELECT ON src TO "+role, 0))
			run(referenceExec("SET SESSION AUTHORIZATION "+role, 0))
			run(referenceExec("CREATE TABLE clone(LIKE src INCLUDING ALL)", 0))
			run(referenceExec("INSERT INTO clone DEFAULT VALUES", 1))
			run(tableLikeError("DROP TABLE clone,src CASCADE", referenceDiagnostic{Code: "42501", Message: "must be owner of table src"}))
			run(referenceSelect("SELECT * FROM clone", []string{"a"}, []string{"INT4"}, [][]any{{int64(7)}}))
			run(referenceExec("RESET SESSION AUTHORIZATION", 0))
			if b.pg { run(referenceSelect("SELECT pg_get_userbyid(relowner) AS owner,relacl IS NULL AS default_acl FROM pg_class WHERE oid='clone'::regclass", []string{"owner", "default_acl"}, []string{"NAME", "BOOL"}, [][]any{{role, true}})) }
			run(referenceExec("DROP TABLE src CASCADE", 0))
			run(referenceSelect("SELECT * FROM clone", []string{"a"}, []string{"INT4"}, [][]any{{int64(7)}}))
			if b.pg { run(referenceExec("REVOKE USAGE,CREATE ON SCHEMA public FROM "+role, 0)) }
			run(tableLikeError("DROP ROLE "+role, referenceDiagnostic{Code: "2BP01", Message: `role "` + role + `" cannot be dropped because some objects depend on it`, Detail: "owner of table clone"}))
			run(referenceExec("DROP TABLE clone", 0))
		run(referenceExec("DROP SCHEMA like_private CASCADE", 0))
		run(referenceExec("DROP ROLE "+role, 0))
	})
}
