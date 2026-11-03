package reference_test

import (
	"context"
	"database/sql"
	"testing"

	"stuntgres/reference/fixture"
)

func TestReferencePGStatSLRUPrivileges(t *testing.T) {
	fixture.RoleReferenceCleanup(t, []string{"slru_reader", "slru_monitor", "slru_stats"})
	activityScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
		for _, query := range []string{"CREATE ROLE slru_reader", "CREATE ROLE slru_monitor", "CREATE ROLE slru_stats", "GRANT pg_monitor TO slru_monitor", "GRANT pg_read_all_stats TO slru_stats"} {
			statsExec(t, p, b, c, query, 0)
		}
		defer func() {
			c.ExecContext(context.Background(), "RESET ROLE")
			c.ExecContext(context.Background(), "GRANT SELECT ON pg_catalog.pg_stat_slru TO PUBLIC")
			c.ExecContext(context.Background(), "GRANT EXECUTE ON FUNCTION pg_catalog.pg_stat_get_slru() TO PUBLIC")
			c.ExecContext(context.Background(), "REVOKE SELECT (name) ON pg_catalog.pg_stat_slru FROM slru_reader")
			c.ExecContext(context.Background(), "REVOKE EXECUTE ON FUNCTION pg_catalog.pg_stat_reset_slru(text) FROM slru_reader")
		}()
		held := slruHeld(t, p, c, slruSQL)
		defer held.Statement.Close()
		const resetSQL = "SELECT pg_catalog.pg_stat_reset_slru($1::text)"
		resetHeld := slruHeld(t, p, c, resetSQL)
		defer resetHeld.Statement.Close()
		before := slruGet(t, p, b, c)
		denied := fixture.ErrorDiagnostic(resetSQL, activityDiagnostic("42501", "permission denied for function pg_stat_reset_slru", 0), "other")
		denied.SelectRows, denied.ErrorStage = true, "execute"
		for _, role := range []string{"slru_reader", "slru_monitor", "slru_stats"} {
			statsExec(t, p, b, c, "SET ROLE "+role, 0)
			// PUBLIC SELECT is not a dynamic-session mask: all nine values survive.
			slruSame(t, before, slruGet(t, p, b, c))
			slruSame(t, before, slruGet(t, p, b, held))
			fixture.RunStep(t, p.Ctx, c, b.Recorder, denied)
			fixture.RunStep(t, p.Ctx, resetHeld, b.Recorder, denied)
			slruSame(t, before, slruGet(t, p, b, held))
			statsExec(t, p, b, c, "RESET ROLE", 0)
		}
		statsExec(t, p, b, c, "GRANT EXECUTE ON FUNCTION pg_catalog.pg_stat_reset_slru(text) TO slru_reader", 0)
		lower := slruDistinctClock(t, p, c, before.rows["other"].reset)
		statsExec(t, p, b, c, "SET ROLE slru_reader", 0)
		resetReplicationVoid(t, p, b, resetHeld, resetSQL, "pg_stat_reset_slru", "other")
		after := slruGet(t, p, b, held)
		slruEvent(t, before, after, lower, slruClock(t, p, c))
		statsExec(t, p, b, c, "RESET ROLE", 0)
		statsExec(t, p, b, c, "REVOKE EXECUTE ON FUNCTION pg_catalog.pg_stat_reset_slru(text) FROM slru_reader", 0)
		statsExec(t, p, b, c, "SET ROLE slru_reader", 0)
		fixture.RunStep(t, p.Ctx, resetHeld, b.Recorder, denied)
		slruSame(t, after, slruGet(t, p, b, held))
		statsExec(t, p, b, c, "RESET ROLE", 0)

		// A retained prepared SELECT must consult current relation/column ACLs,
		// not authority captured while its connection was superuser.
		statsExec(t, p, b, c, "REVOKE SELECT ON pg_catalog.pg_stat_slru FROM PUBLIC", 0)
		statsExec(t, p, b, c, "SET ROLE slru_reader", 0)
		selectDenied := fixture.ErrorDiagnostic(slruSQL, activityDiagnostic("42501", "permission denied for view pg_stat_slru", 0))
		selectDenied.SelectRows, selectDenied.ErrorStage = true, "execute"
		fixture.RunStep(t, p.Ctx, c, b.Recorder, selectDenied)
		fixture.RunStep(t, p.Ctx, held, b.Recorder, selectDenied)
		statsExec(t, p, b, c, "RESET ROLE", 0)
		statsExec(t, p, b, c, "GRANT SELECT (name) ON pg_catalog.pg_stat_slru TO slru_reader", 0)
		statsExec(t, p, b, c, "SET ROLE slru_reader", 0)
		fixture.RunStep(t, p.Ctx, c, b.Recorder, fixture.Select("SELECT name FROM pg_catalog.pg_stat_slru ORDER BY name", []string{"name"}, []string{"TEXT"}, [][]any{{"commit_timestamp"},{"multixact_member"},{"multixact_offset"},{"notify"},{"other"},{"serializable"},{"subtransaction"},{"transaction"}}))
		for _, query := range []string{"SELECT name FROM pg_stat_slru WHERE blks_hit=0", "SELECT name FROM pg_stat_slru ORDER BY blks_hit"} {
			step := fixture.ErrorDiagnostic(query, activityDiagnostic("42501", "permission denied for view pg_stat_slru", 0)); step.SelectRows, step.ErrorStage = true, "execute"
			fixture.RunStep(t, p.Ctx, c, b.Recorder, step)
		}
		fixture.RunStep(t, p.Ctx, held, b.Recorder, selectDenied)
		statsExec(t, p, b, c, "RESET ROLE", 0)
		statsExec(t, p, b, c, "REVOKE SELECT (name) ON pg_catalog.pg_stat_slru FROM slru_reader", 0)
		statsExec(t, p, b, c, "GRANT SELECT ON pg_catalog.pg_stat_slru TO PUBLIC", 0)
		statsExec(t, p, b, c, "SET ROLE slru_reader", 0)
		slruSame(t, after, slruGet(t, p, b, held))
		statsExec(t, p, b, c, "RESET ROLE", 0)

		// The descriptive getter has independent live EXECUTE authority even
		// though the embedded SQL evaluator does not expose a callable getter.
		statsExec(t, p, b, c, "REVOKE EXECUTE ON FUNCTION pg_catalog.pg_stat_get_slru() FROM PUBLIC", 0)
		statsExec(t, p, b, c, "SET ROLE slru_reader", 0)
		getterDenied := fixture.ErrorDiagnostic(slruSQL, activityDiagnostic("42501", "permission denied for function pg_stat_get_slru", 0))
		getterDenied.SelectRows, getterDenied.ErrorStage = true, "execute"
		fixture.RunStep(t, p.Ctx, c, b.Recorder, getterDenied)
		fixture.RunStep(t, p.Ctx, held, b.Recorder, getterDenied)
		statsExec(t, p, b, c, "RESET ROLE", 0)
		statsExec(t, p, b, c, "GRANT EXECUTE ON FUNCTION pg_catalog.pg_stat_get_slru() TO PUBLIC", 0)
		statsExec(t, p, b, c, "SET ROLE slru_reader", 0)
		slruSame(t, after, slruGet(t, p, b, held))
		statsExec(t, p, b, c, "RESET ROLE", 0)
	})
}
