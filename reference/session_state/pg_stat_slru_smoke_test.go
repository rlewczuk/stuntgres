package reference_test

import (
	"context"
	"database/sql"
	"testing"

	"stuntgres/reference/fixture"
)

func TestReferencePGStatSLRUFeatureSmoke(t *testing.T) {
	fixture.RoleReferenceCleanup(t, []string{"slru_smoke_reader"})
	activityScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
		defer c.ExecContext(context.Background(), "RESET ALL")
		defer c.ExecContext(context.Background(), "RESET ROLE")
		statsExec(t, p, b, c, "SET stats_fetch_consistency=none", 0)
		before := slruGet(t, p, b, c)
		held := slruHeld(t, p, c, slruSQL)
		defer held.Statement.Close()
		slruSame(t, before, slruGet(t, p, b, held))
		statsExec(t, p, b, c, "CREATE TABLE slru_sentinel(v integer)", 0)
		statsExec(t, p, b, c, "INSERT INTO slru_sentinel VALUES(37)", 1)
		slruSentinel(t, p, b, c)
		slruSame(t, before, slruGet(t, p, b, held))
		lower := slruDistinctClock(t, p, c, before.rows["other"].reset)
		resetReplicationVoid(t, p, b, c, "SELECT pg_stat_reset_slru('other')", "pg_stat_reset_slru")
		after := slruGet(t, p, b, held)
		slruEvent(t, before, after, lower, slruClock(t, p, c))
		statsExec(t, p, b, c, "CREATE ROLE slru_smoke_reader", 0)
		statsExec(t, p, b, c, "SET ROLE slru_smoke_reader", 0)
		slruSame(t, after, slruGet(t, p, b, c))
		slruSame(t, after, slruGet(t, p, b, held))
		statsExec(t, p, b, c, "RESET ROLE", 0)
		step := fixture.ErrorDiagnostic("UPDATE pg_catalog.pg_stat_slru SET blks_hit=1 WHERE FALSE", slruMutationDiagnostic("UPDATE"))
		step.ErrorStage = "execute"
		fixture.RunStep(t, p.Ctx, c, b.Recorder, step)
		slruSame(t, after, slruGet(t, p, b, held))
		slruSentinel(t, p, b, c)
		for _, mode := range []string{"cache", "snapshot"} { slruConsistency(t, p, b, c, mode) }
		if !b.PG {
			slruNativeControls(t, p, b, c, held)
		}
	})
}
