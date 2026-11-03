package reference_test

import (
	"context"
	"database/sql"
	"testing"

	"stuntgres/reference/fixture"
)

func TestReferencePGStatRecoveryPrefetchFeatureSmoke(t *testing.T) {
	fixture.RoleReferenceCleanup(t, []string{"prefetch_smoke_reader"})
	activityScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
		defer c.ExecContext(context.Background(), "RESET ALL")
		defer c.ExecContext(context.Background(), "RESET ROLE")
		before := prefetchGet(t, p, b, c)
		held := prefetchHeld(t, p, c, prefetchSQL)
		defer held.Statement.Close()
		prefetchSame(t, before, prefetchGet(t, p, b, held))
		statsExec(t, p, b, c, "CREATE TABLE prefetch_sentinel(v integer)", 0)
		statsExec(t, p, b, c, "INSERT INTO prefetch_sentinel VALUES(37)", 1)
		prefetchSentinel(t, p, b, c)
		prefetchSame(t, before, prefetchGet(t, p, b, held))
		lower := prefetchClock(t, p, c)
		resetReplicationVoid(t, p, b, c, "SELECT pg_stat_reset_shared('recovery_prefetch')", "pg_stat_reset_shared")
		after := prefetchGet(t, p, b, held)
		prefetchEvent(t, before, after, lower, prefetchClock(t, p, c))
		statsExec(t, p, b, c, "CREATE ROLE prefetch_smoke_reader", 0)
		statsExec(t, p, b, c, "SET ROLE prefetch_smoke_reader", 0)
		prefetchSame(t, after, prefetchGet(t, p, b, c))
		prefetchSame(t, after, prefetchGet(t, p, b, held))
		statsExec(t, p, b, c, "RESET ROLE", 0)
		step := fixture.ErrorDiagnostic("UPDATE pg_catalog.pg_stat_recovery_prefetch SET prefetch=1 WHERE FALSE", prefetchMutationDiagnostic("UPDATE"))
		step.ErrorStage = "execute"
		fixture.RunStep(t, p.Ctx, c, b.Recorder, step)
		prefetchSame(t, after, prefetchGet(t, p, b, held))
		prefetchSentinel(t, p, b, c)
		if !b.PG {
			prefetchNativeControls(t, p, b, c, held)
		}
	})
}
