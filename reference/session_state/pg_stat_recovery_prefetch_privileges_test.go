package reference_test

import (
	"context"
	"database/sql"
	"testing"

	"stuntgres/reference/fixture"
)

func TestReferencePGStatRecoveryPrefetchPrivileges(t *testing.T) {
	fixture.RoleReferenceCleanup(t, []string{"prefetch_reader", "prefetch_monitor", "prefetch_stats"})
	activityScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
		for _, query := range []string{"CREATE ROLE prefetch_reader", "CREATE ROLE prefetch_monitor", "CREATE ROLE prefetch_stats", "GRANT pg_monitor TO prefetch_monitor", "GRANT pg_read_all_stats TO prefetch_stats"} {
			statsExec(t, p, b, c, query, 0)
		}
		defer func() {
			c.ExecContext(context.Background(), "RESET ROLE")
			c.ExecContext(context.Background(), "GRANT SELECT ON pg_catalog.pg_stat_recovery_prefetch TO PUBLIC")
			c.ExecContext(context.Background(), "REVOKE SELECT (stats_reset) ON pg_catalog.pg_stat_recovery_prefetch FROM prefetch_reader")
			c.ExecContext(context.Background(), "REVOKE EXECUTE ON FUNCTION pg_catalog.pg_stat_reset_shared(text) FROM prefetch_reader")
		}()
		held := prefetchHeld(t, p, c, prefetchSQL)
		defer held.Statement.Close()
		const resetSQL = "SELECT pg_catalog.pg_stat_reset_shared($1::text)"
		resetHeld := prefetchHeld(t, p, c, resetSQL)
		defer resetHeld.Statement.Close()
		before := prefetchGet(t, p, b, c)
		denied := fixture.ErrorDiagnostic(resetSQL, activityDiagnostic("42501", "permission denied for function pg_stat_reset_shared", 0), "recovery_prefetch")
		denied.SelectRows, denied.ErrorStage = true, "execute"
		for _, role := range []string{"prefetch_reader", "prefetch_monitor", "prefetch_stats"} {
			statsExec(t, p, b, c, "SET ROLE "+role, 0)
			// PUBLIC SELECT is not a dynamic-session mask: all ten values survive.
			prefetchSame(t, before, prefetchGet(t, p, b, c))
			prefetchSame(t, before, prefetchGet(t, p, b, held))
			fixture.RunStep(t, p.Ctx, c, b.Recorder, denied)
			fixture.RunStep(t, p.Ctx, resetHeld, b.Recorder, denied)
			prefetchSame(t, before, prefetchGet(t, p, b, held))
			statsExec(t, p, b, c, "RESET ROLE", 0)
		}
		statsExec(t, p, b, c, "GRANT EXECUTE ON FUNCTION pg_catalog.pg_stat_reset_shared(text) TO prefetch_reader", 0)
		lower := prefetchClock(t, p, c)
		statsExec(t, p, b, c, "SET ROLE prefetch_reader", 0)
		resetReplicationVoid(t, p, b, resetHeld, resetSQL, "pg_stat_reset_shared", "recovery_prefetch")
		after := prefetchGet(t, p, b, held)
		prefetchEvent(t, before, after, lower, prefetchClock(t, p, c))
		statsExec(t, p, b, c, "RESET ROLE", 0)
		statsExec(t, p, b, c, "REVOKE EXECUTE ON FUNCTION pg_catalog.pg_stat_reset_shared(text) FROM prefetch_reader", 0)
		statsExec(t, p, b, c, "SET ROLE prefetch_reader", 0)
		fixture.RunStep(t, p.Ctx, resetHeld, b.Recorder, denied)
		prefetchSame(t, after, prefetchGet(t, p, b, held))
		statsExec(t, p, b, c, "RESET ROLE", 0)

		// A retained prepared SELECT must consult current relation/column ACLs,
		// not authority captured while its connection was superuser.
		statsExec(t, p, b, c, "REVOKE SELECT ON pg_catalog.pg_stat_recovery_prefetch FROM PUBLIC", 0)
		statsExec(t, p, b, c, "SET ROLE prefetch_reader", 0)
		selectDenied := fixture.ErrorDiagnostic(prefetchSQL, activityDiagnostic("42501", "permission denied for view pg_stat_recovery_prefetch", 0))
		selectDenied.SelectRows, selectDenied.ErrorStage = true, "execute"
		fixture.RunStep(t, p.Ctx, c, b.Recorder, selectDenied)
		fixture.RunStep(t, p.Ctx, held, b.Recorder, selectDenied)
		statsExec(t, p, b, c, "RESET ROLE", 0)
		statsExec(t, p, b, c, "GRANT SELECT (stats_reset) ON pg_catalog.pg_stat_recovery_prefetch TO prefetch_reader", 0)
		statsExec(t, p, b, c, "SET ROLE prefetch_reader", 0)
		var reset sql.NullTime
		if err := c.QueryRowContext(p.Ctx, "SELECT stats_reset FROM pg_catalog.pg_stat_recovery_prefetch").Scan(&reset); err != nil || !reset.Valid || !reset.Time.Equal(after.reset) {
			t.Fatal("column ACL read", reset, err)
		}
		fixture.RunStep(t, p.Ctx, held, b.Recorder, selectDenied)
		statsExec(t, p, b, c, "RESET ROLE", 0)
		statsExec(t, p, b, c, "REVOKE SELECT (stats_reset) ON pg_catalog.pg_stat_recovery_prefetch FROM prefetch_reader", 0)
		statsExec(t, p, b, c, "GRANT SELECT ON pg_catalog.pg_stat_recovery_prefetch TO PUBLIC", 0)
		statsExec(t, p, b, c, "SET ROLE prefetch_reader", 0)
		prefetchSame(t, after, prefetchGet(t, p, b, held))
		statsExec(t, p, b, c, "RESET ROLE", 0)
	})
}
