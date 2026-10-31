package reference_test

import (
	"database/sql"
	"testing"

	"stuntgres/reference/fixture"
)

func TestReferenceLoManagePrivileges(t *testing.T) {
	t.Run("owner_required", func(t *testing.T) {
		fixture.RoleReferenceCleanup(t, []string{"lm_actor"})
		lmCase(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn, prepared bool) {
			lmSetup(t, p, b, c, "lo", "v", "UPDATE OR DELETE", "ROW")
			loRun(t, p, b, c, fixture.Exec("CREATE ROLE lm_actor", 0))
			defer func() {
				if _, err := c.ExecContext(p.Ctx, "RESET ROLE"); err != nil {
					t.Error(err)
					return
				}
				for _, q := range []string{
					"DROP TABLE IF EXISTS public.lm",
					"SELECT lo_unlink(oid) FROM pg_largeobject_metadata WHERE oid IN (910201::oid,910202::oid)",
					"REVOKE USAGE ON SCHEMA public FROM lm_actor",
					"DROP ROLE lm_actor",
				} {
					if _, err := c.ExecContext(p.Ctx, q); err != nil {
						t.Error(err)
					}
				}
			}()
			loRun(t, p, b, c, fixture.Exec("GRANT USAGE ON SCHEMA public TO lm_actor", 0))
			loRun(t, p, b, c, fixture.Exec("ALTER TABLE lm OWNER TO lm_actor", 0))
			loRun(t, p, b, c, fixture.Exec("GRANT SELECT,UPDATE ON LARGE OBJECT 910201,910202 TO lm_actor", 0))
			loRun(t, p, b, c, fixture.Exec("SET ROLE lm_actor", 0))
			lmRun(t, p, b, c, prepared, fixture.Exec("UPDATE lm SET v=v", 1))
			for _, q := range []string{"DELETE FROM lm", "UPDATE lm SET v=910202"} {
				lmRun(t, p, b, c, prepared, lmError(q, "42501", "must be owner of large object 910201"))
				lmRows(t, p, b, c, lmRow(int64(910201), 0))
				lmBytes(t, p, b, c, 910201, "00ff")
			}
			loRun(t, p, b, c, fixture.Exec("RESET ROLE", 0))
			lmRows(t, p, b, c, lmRow(int64(910201), 0))
			lmBytes(t, p, b, c, 910201, "00ff")
		})
	})
	t.Run("direct_unlink_body", func(t *testing.T) {
		fixture.RoleReferenceCleanup(t, []string{"lm_owner"})
		lmCase(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn, prepared bool) {
			lmSetup(t, p, b, c, "lo", "v", "UPDATE OR DELETE", "ROW")
			loRun(t, p, b, c, fixture.Exec("CREATE ROLE lm_owner", 0))
			defer func() {
				if _, err := c.ExecContext(p.Ctx, "RESET ROLE"); err != nil {
					t.Error(err)
					return
				}
				if _, err := c.ExecContext(p.Ctx, "GRANT EXECUTE ON FUNCTION pg_catalog.lo_unlink(oid) TO PUBLIC"); err != nil {
					t.Error(err)
				}
				for _, q := range []string{
					"DROP TABLE IF EXISTS public.lm",
					"SELECT lo_unlink(oid) FROM pg_largeobject_metadata WHERE oid IN (910201::oid,910202::oid)",
					"REVOKE USAGE ON SCHEMA public FROM lm_owner",
					"DROP ROLE lm_owner",
				} {
					if _, err := c.ExecContext(p.Ctx, q); err != nil {
						t.Error(err)
					}
				}
			}()
			loRun(t, p, b, c, fixture.Exec("GRANT USAGE ON SCHEMA public TO lm_owner", 0))
			loRun(t, p, b, c, fixture.Exec("ALTER TABLE lm OWNER TO lm_owner", 0))
			loRun(t, p, b, c, fixture.Exec("ALTER LARGE OBJECT 910201 OWNER TO lm_owner", 0))
			loRun(t, p, b, c, fixture.Exec("REVOKE EXECUTE ON FUNCTION pg_catalog.lo_unlink(oid) FROM PUBLIC", 0))
			loRun(t, p, b, c, fixture.Exec("SET ROLE lm_owner", 0))
			lmRun(t, p, b, c, prepared, fixture.Exec("DELETE FROM lm", 1))
			lmRows(t, p, b, c)
			lmMissing(t, p, b, c, 910201)
			loRun(t, p, b, c, fixture.Exec("RESET ROLE", 0))
		})
	})
}
