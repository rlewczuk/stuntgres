package reference_test

import (
	"context"
	"database/sql"
	"testing"

	"stuntgres/reference/fixture"
)

func TestReferencePGStatReplicationSlotsPrivileges(t *testing.T) {
	roles := []string{"stat_slots_reader", "stat_slots_repl", "stat_slots_stats", "stat_slots_monitor"}
	fixture.RoleReferenceCleanup(t, roles)
	activityScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
		for _, query := range []string{"CREATE ROLE stat_slots_reader", "CREATE ROLE stat_slots_repl REPLICATION", "CREATE ROLE stat_slots_stats", "CREATE ROLE stat_slots_monitor", "GRANT pg_read_all_stats TO stat_slots_stats", "GRANT pg_monitor TO stat_slots_monitor"} {
			statsExec(t, p, b, c, query, 0)
		}
		defer func() {
			c.ExecContext(context.Background(), "RESET ROLE")
			c.ExecContext(context.Background(), "GRANT SELECT ON pg_catalog.pg_stat_replication_slots TO PUBLIC")
		}()
		held := prefetchHeld(t, p, c, replicationSlotsSQL)
		defer held.Statement.Close()
		replicationSlotsEmpty(t, p, b, c)
		replicationSlotsEmpty(t, p, b, held)
		for _, role := range roles {
			statsExec(t, p, b, c, "SET ROLE "+role, 0)
			replicationSlotsEmpty(t, p, b, c)
			replicationSlotsEmpty(t, p, b, held)
			fresh := prefetchHeld(t, p, c, replicationSlotsSQL)
			replicationSlotsEmpty(t, p, b, fresh)
			fresh.Statement.Close()
			statsExec(t, p, b, c, "RESET ROLE", 0)
		}
		statsExec(t, p, b, c, "REVOKE SELECT ON pg_catalog.pg_stat_replication_slots FROM PUBLIC", 0)
		statsExec(t, p, b, c, "SET ROLE stat_slots_reader", 0)
		denied := fixture.ErrorDiagnostic(replicationSlotsSQL, activityDiagnostic("42501", "permission denied for view pg_stat_replication_slots", 0))
		denied.SelectRows, denied.ErrorStage = true, "execute"
		fixture.RunStep(t, p.Ctx, c, b.Recorder, denied)
		fixture.RunStep(t, p.Ctx, held, b.Recorder, denied)
		statsExec(t, p, b, c, "RESET ROLE", 0)
		statsExec(t, p, b, c, "GRANT SELECT ON pg_catalog.pg_stat_replication_slots TO PUBLIC", 0)
		statsExec(t, p, b, c, "SET ROLE stat_slots_reader", 0)
		replicationSlotsEmpty(t, p, b, c)
		replicationSlotsEmpty(t, p, b, held)
		statsExec(t, p, b, c, "RESET ROLE", 0)
	})
}
