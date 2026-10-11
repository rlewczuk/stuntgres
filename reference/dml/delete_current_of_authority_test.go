package reference_test

import (
	"database/sql"
	"stuntgres/reference/fixture"
	"testing"
)

func currentOfAuthority(t *testing.T) {
	const role = "current_of_actor"
	for _, prepared := range []bool{false, true} {
		t.Run(map[bool]string{false: "direct", true: "prepared"}[prepared], func(t *testing.T) {
			fixture.RoleReferenceCleanup(t, []string{role})
			currentOfReference(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
				run := updateFromRun(t, p, b, c)
				currentOfSeed(run)
				if b.PG {
					run(fixture.Exec("GRANT USAGE ON SCHEMA public TO PUBLIC", 0))
				}
				run(fixture.Exec("CREATE USER "+role, 0))
				run(fixture.Exec("GRANT SELECT ON TABLE t TO "+role, 0))
				run(fixture.Exec("SET SESSION AUTHORIZATION "+role, 0))
				defer c.ExecContext(p.Ctx, "RESET SESSION AUTHORIZATION")
				run(fixture.Exec("BEGIN", 0))
				defer c.ExecContext(p.Ctx, "ROLLBACK")
				run(fixture.Exec("DECLARE c CURSOR FOR SELECT id FROM t WHERE id=1", 0))
				run(currentOfFetch("FETCH FORWARD 1 FROM c", 1))
				for _, name := range []string{"missing", "c"} {
					currentOfFailure(t, p, b, c, currentOfError("DELETE FROM t WHERE CURRENT OF "+name, "42501", "permission denied for table t", prepared))
				}
				run(fixture.Exec("ROLLBACK", 0))
				run(fixture.Exec("RESET SESSION AUTHORIZATION", 0))
				run(fixture.Exec("GRANT DELETE ON TABLE t TO "+role, 0))
				run(fixture.Exec("SET SESSION AUTHORIZATION "+role, 0))
				run(fixture.Exec("BEGIN", 0))
				run(fixture.Exec("DECLARE c CURSOR FOR SELECT id FROM t WHERE id=1", 0))
				run(currentOfFetch("FETCH FORWARD 1 FROM c", 1))
				// DELETE reads no cursor projection columns for authorization. Revoking the
				// original FETCH SELECT grant does not transfer or revoke DELETE authority.
				admin, err := b.DB.Conn(p.Ctx)
				if err != nil {
					t.Fatal(err)
				}
				defer admin.Close()
				fixture.LockExec(t, p.Ctx, admin, "REVOKE SELECT ON TABLE t FROM "+role, 0)
				s := fixture.Exec("DELETE FROM t WHERE CURRENT OF c", 1)
				s.Prepared = prepared
				run(s)
				run(fixture.Exec("ROLLBACK", 0))
				run(fixture.Exec("RESET SESSION AUTHORIZATION", 0))
				fixture.DeleteReferenceRows(t, p, c, b.Recorder, 1, 2, 3)
			})
		})
	}
}
