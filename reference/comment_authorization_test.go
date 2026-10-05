//go:build reference

package reference_test

import (
	"context"
	"fmt"
	"testing"
)

// GRANT/catalog setup is oracle-only; native authority is exercised through the
// real internal ACL seam in TestCommentAuthorizationAndLookup, not fake SQL.
func TestReferenceCommentSchemaUsage(t *testing.T) {
	for _, prepared := range []bool{false, true} {
		t.Run(fmt.Sprint(prepared), func(t *testing.T) {
			role := "comment_acl_" + fmt.Sprint(prepared)
			roleReferenceCleanup(t, []string{role})
			p := newReferencePair(t)
			p.step(t, referenceExec("CREATE USER "+role, 0))
			if _, err := referenceAdmin.ExecContext(p.ctx, "CREATE SCHEMA comment_private; CREATE TABLE comment_private.t(v INT); REVOKE ALL ON SCHEMA comment_private FROM PUBLIC;"); err != nil {
				t.Fatal(err)
			}
			t.Cleanup(func() {
				if _, err := referenceAdmin.ExecContext(context.Background(), "DROP SCHEMA IF EXISTS comment_private CASCADE"); err != nil {
					t.Error(err)
				}
			})
			conn, err := p.postgres.Conn(p.ctx)
			if err != nil {
				t.Fatal(err)
			}
			defer conn.Close()
			run := func(q, code, message string) {
				s := roleReferenceError(q, code, message, "", prepared, "execute")
				referenceRunStep(t, p.ctx, conn, p.postgresRecord, s)
			}
			if _, err := conn.ExecContext(p.ctx, "SET SESSION AUTHORIZATION "+role); err != nil {
				t.Fatal(err)
			}
			run("COMMENT ON TABLE comment_private.missing IS NULL", "42501", "permission denied for schema comment_private")
			run("COMMENT ON COLUMN comment_private.t.missing IS NULL", "42501", "permission denied for schema comment_private")
			if _, err := conn.ExecContext(p.ctx, "SET search_path TO comment_private"); err != nil {
				t.Fatal(err)
			}
			run("COMMENT ON TABLE t IS NULL", "42P01", `relation "t" does not exist`)
			if _, err := referenceAdmin.ExecContext(p.ctx, "GRANT USAGE, CREATE ON SCHEMA comment_private TO "+role+"; GRANT SELECT,INSERT ON comment_private.t TO "+role); err != nil {
				t.Fatal(err)
			}
			run("COMMENT ON TABLE comment_private.t IS NULL", "42501", "must be owner of table t")
			run("COMMENT ON COLUMN comment_private.t.v IS NULL", "42501", "must be owner of relation t")
			run("COMMENT ON COLUMN comment_private.t.missing IS NULL", "42703", `column "missing" of relation "comment_private.t" does not exist`)
		})
	}
}
