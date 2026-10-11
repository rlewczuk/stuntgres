//go:build reference

package reference_test

import (
	"context"
	"fmt"
	"stuntgres/reference/fixture"
	"testing"
)

// GRANT/catalog setup is oracle-only; native authority is exercised through the
// real internal ACL seam in TestCommentAuthorizationAndLookup, not fake SQL.
func TestReferenceCommentSchemaUsage(t *testing.T) {
	for _, prepared := range []bool{false, true} {
		t.Run(fmt.Sprint(prepared), func(t *testing.T) {
			role := "comment_acl_" + fmt.Sprint(prepared)
			fixture.RoleReferenceCleanup(t, []string{role})
			p := fixture.NewPair(t)
			p.Step(t, fixture.Exec("CREATE USER "+role, 0))
			if _, err := fixture.Admin.ExecContext(p.Ctx, "CREATE SCHEMA comment_private; CREATE TABLE comment_private.t(v INT); REVOKE ALL ON SCHEMA comment_private FROM PUBLIC;"); err != nil {
				t.Fatal(err)
			}
			t.Cleanup(func() {
				if _, err := fixture.Admin.ExecContext(context.Background(), "DROP SCHEMA IF EXISTS comment_private CASCADE"); err != nil {
					t.Error(err)
				}
			})
			conn, err := p.Postgres.Conn(p.Ctx)
			if err != nil {
				t.Fatal(err)
			}
			defer conn.Close()
			run := func(q, code, message string) {
				s := fixture.RoleReferenceError(q, code, message, "", prepared, "execute")
				fixture.RunStep(t, p.Ctx, conn, p.PostgresRecord, s)
			}
			if _, err := conn.ExecContext(p.Ctx, "SET SESSION AUTHORIZATION "+role); err != nil {
				t.Fatal(err)
			}
			run("COMMENT ON TABLE comment_private.missing IS NULL", "42501", "permission denied for schema comment_private")
			run("COMMENT ON COLUMN comment_private.t.missing IS NULL", "42501", "permission denied for schema comment_private")
			if _, err := conn.ExecContext(p.Ctx, "SET search_path TO comment_private"); err != nil {
				t.Fatal(err)
			}
			run("COMMENT ON TABLE t IS NULL", "42P01", `relation "t" does not exist`)
			if _, err := fixture.Admin.ExecContext(p.Ctx, "GRANT USAGE, CREATE ON SCHEMA comment_private TO "+role+"; GRANT SELECT,INSERT ON comment_private.t TO "+role); err != nil {
				t.Fatal(err)
			}
			run("COMMENT ON TABLE comment_private.t IS NULL", "42501", "must be owner of table t")
			run("COMMENT ON COLUMN comment_private.t.v IS NULL", "42501", "must be owner of relation t")
			run("COMMENT ON COLUMN comment_private.t.missing IS NULL", "42703", `column "missing" of relation "comment_private.t" does not exist`)
		})
	}
}
