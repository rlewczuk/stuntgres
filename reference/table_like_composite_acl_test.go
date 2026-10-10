package reference_test

import (
	"context"
	"database/sql"
	"fmt"
	"testing"
)

func TestReferenceTableLikeCompositeSourceACL(t *testing.T) {
	tableLikeScenario(t, func(t *testing.T, p *referencePair, b referenceBackend, c *sql.Conn, prepared bool, run func(referenceStep)) {
		// GRANT/REVOKE is the existing PostgreSQL-only authority setup; native
		// type-versus-backing-relation ACLs use the engine descriptor seam.
		if !b.pg {
			return
		}
		role := fmt.Sprintf("composite_like_reader_%t", prepared)
		roleReferenceCleanup(t, []string{role})
		defer c.ExecContext(context.Background(), "RESET SESSION AUTHORIZATION")
		run(referenceExec("CREATE USER "+role, 0))
		run(referenceExec("GRANT USAGE,CREATE ON SCHEMA public TO "+role, 0))
		run(referenceExec("CREATE TYPE source_type AS (a INT,b TEXT)", 0))
		run(referenceExec("REVOKE USAGE ON TYPE source_type FROM PUBLIC", 0))
		run(referenceExec("CREATE SCHEMA like_composite_private", 0))
		defer b.db.ExecContext(context.Background(), "DROP SCHEMA IF EXISTS like_composite_private CASCADE")
		run(referenceExec("CREATE TYPE like_composite_private.source_type AS (a INT)", 0))
		run(referenceExec("SET SESSION AUTHORIZATION "+role, 0))
		run(tableLikeError("CREATE TABLE clone (LIKE source_type INCLUDING ALL)", referenceDiagnostic{Code: "42501", Message: "permission denied for type source_type"}))
		run(tableLikeError("CREATE TABLE clone (LIKE like_composite_private.source_type INCLUDING ALL)", referenceDiagnostic{Code: "42501", Message: "permission denied for schema like_composite_private", Position: "26"}))
		run(referenceExec("RESET SESSION AUTHORIZATION", 0))
		run(referenceExec("GRANT USAGE ON TYPE source_type TO "+role, 0))
		// No SELECT privilege on the backing relation is granted. Type USAGE alone
		// authorizes the source; destination privileges belong to its new owner.
		run(referenceExec("SET SESSION AUTHORIZATION "+role, 0))
		run(referenceExec("CREATE TABLE clone(LIKE source_type INCLUDING ALL)", 0))
		run(referenceExec("INSERT INTO clone VALUES(7,'reader')", 1))
		run(referenceSelect("SELECT * FROM clone", []string{"a", "b"}, []string{"INT4", "TEXT"}, [][]any{{int64(7), "reader"}}))
		run(referenceExec("RESET SESSION AUTHORIZATION", 0))
		run(referenceSelect("SELECT pg_get_userbyid(relowner) AS owner,relacl IS NULL AS default_acl FROM pg_class WHERE oid='clone'::regclass", []string{"owner", "default_acl"}, []string{"NAME", "BOOL"}, [][]any{{role, true}}))
		run(referenceExec("DROP TYPE source_type", 0))
		run(referenceExec("INSERT INTO clone VALUES(NULL,NULL)", 1))
		run(referenceExec("DROP TABLE clone", 0))
		run(referenceExec("DROP SCHEMA like_composite_private CASCADE", 0))
		run(referenceExec("REVOKE USAGE,CREATE ON SCHEMA public FROM "+role, 0))
		run(referenceExec("DROP ROLE "+role, 0))
	})
}
