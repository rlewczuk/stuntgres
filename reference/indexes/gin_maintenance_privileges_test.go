package reference_test

import (
	"strconv"
	"stuntgres/reference/fixture"
	"testing"
)

func TestReferenceGINMaintenancePrivileges(t *testing.T) {
	fixture.RoleReferenceCleanup(t, []string{"gin_maint_owner", "gin_maint_member", "gin_maint_maintainer"})
	classBackends(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend) {
		conn, run := classConn(t, p, b)
		defer conn.ExecContext(p.Ctx, "RESET ROLE")
		ginMaintSeed(run, false)
		for _, role := range []string{"gin_maint_owner", "gin_maint_member", "gin_maint_maintainer"} {
			run(fixture.Exec("CREATE ROLE "+role, 0))
		}
		run(fixture.Exec("GRANT USAGE,CREATE ON SCHEMA public TO gin_maint_owner", 0))
		run(fixture.Exec("GRANT USAGE ON SCHEMA public TO gin_maint_member,gin_maint_maintainer", 0))
		run(fixture.Exec("ALTER TABLE gin_maint_oracle OWNER TO gin_maint_owner", 0))
		run(fixture.Exec("GRANT gin_maint_owner TO gin_maint_member", 0))
		run(fixture.Exec("GRANT pg_maintain TO gin_maint_maintainer", 0))
		run(fixture.Exec("GRANT SELECT,MAINTAIN ON gin_maint_oracle TO gin_maint_maintainer", 0))
		run(fixture.Exec("SET ROLE gin_maint_maintainer", 0))
		denied := fixture.ErrorDiagnostic(ginMaintCleanSQL, fixture.ControlsReferenceDiagnostic("42501", "must be owner of index gin_maint_oracle_i"))
		denied.ErrorStage = "execute"
		classRoutes(t, p, b, conn, denied)
		fixture.RunStep(t, p.Ctx, classHeld(t, p, conn, ginMaintCleanSQL), b.Recorder, denied)
		run(fixture.Exec("RESET ROLE", 0))
		for i, role := range []string{"gin_maint_owner", "gin_maint_member", "postgres"} {
			if i > 0 {
				run(fixture.Exec("INSERT INTO gin_maint_oracle VALUES("+strconv.Itoa(i+1)+",'{2}')", 1))
			}
			run(fixture.Exec("SET ROLE "+role, 0))
			run(ginMaintPositive(ginMaintPositiveSQL))
			run(ginMaintResult(ginMaintCleanSQL, int64(0)))
			run(fixture.Exec("RESET ROLE", 0))
		}
		run(fixture.Exec("INSERT INTO gin_maint_oracle VALUES(9,'{2}')", 1))
		// A held call retains a recipe, not EXECUTE authority or a captured root.
		held := classHeld(t, p, conn, ginMaintCleanSQL)
		run(fixture.Exec("REVOKE EXECUTE ON FUNCTION pg_catalog.gin_clean_pending_list(regclass) FROM PUBLIC", 0))
		t.Cleanup(func() {
			_, _ = conn.ExecContext(p.Ctx, "RESET ROLE")
			_, _ = conn.ExecContext(p.Ctx, "GRANT EXECUTE ON FUNCTION pg_catalog.gin_clean_pending_list(regclass) TO PUBLIC")
		})
		run(fixture.Exec("SET ROLE gin_maint_owner", 0))
		denied = fixture.ErrorDiagnostic(ginMaintCleanSQL, fixture.ControlsReferenceDiagnostic("42501", "permission denied for function gin_clean_pending_list"))
		denied.ErrorStage = "execute"
		classRoutes(t, p, b, conn, denied)
		fixture.RunStep(t, p.Ctx, held, b.Recorder, denied)
		run(fixture.Exec("RESET ROLE", 0))
		run(fixture.Exec("GRANT EXECUTE ON FUNCTION pg_catalog.gin_clean_pending_list(regclass) TO gin_maint_owner", 0))
		run(fixture.Exec("SET ROLE gin_maint_owner", 0))
		run(ginMaintPositive(ginMaintPositiveSQL))
		fixture.RunStep(t, p.Ctx, held, b.Recorder, ginMaintResult(ginMaintCleanSQL, int64(0)))
		run(fixture.Exec("RESET ROLE", 0))
		run(fixture.Exec("GRANT EXECUTE ON FUNCTION pg_catalog.gin_clean_pending_list(regclass) TO PUBLIC", 0))
		for _, role := range []string{"gin_maint_member", "gin_maint_maintainer", "gin_maint_owner"} {
			run(fixture.Exec("DROP OWNED BY "+role, 0))
			run(fixture.Exec("DROP ROLE "+role, 0))
		}
	})
}
