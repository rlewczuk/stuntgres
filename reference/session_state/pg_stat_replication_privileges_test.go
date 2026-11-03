package reference_test

import (
	"context"
	"database/sql"
	"testing"

	"stuntgres/reference/fixture"
)

func TestReferencePGStatReplicationPrivileges(t *testing.T) {
	fixture.RoleReferenceCleanup(t, []string{"replication_reader", "replication_stats_reader", "replication_stats_group"})
	activityScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
		for _, query := range []string{
			"CREATE ROLE replication_reader",
			"CREATE ROLE replication_stats_reader INHERIT",
			"CREATE ROLE replication_stats_group",
			"GRANT pg_read_all_stats TO replication_stats_group",
			"GRANT replication_stats_group TO replication_stats_reader",
		} {
			statsExec(t, p, b, c, query, 0)
		}
		defer c.ExecContext(context.Background(), "RESET ROLE")
		held := replicationHeld(t, p, c, replicationSQL)
		defer held.Statement.Close()
		replicationEmpty(t, p, b, c)
		replicationEmpty(t, p, b, held)
		for _, role := range []string{"replication_reader", "replication_stats_reader"} {
			statsExec(t, p, b, c, "SET ROLE "+role, 0)
			// Empty results establish PUBLIC SELECT and absence under current
			// authority, not positive-row redaction: native has no WAL sender.
			replicationEmpty(t, p, b, c)
			replicationEmpty(t, p, b, held)
			fresh := fixture.Prepared(replicationStep(t, b, replicationSQL, replicationAllColumns()))
			fixture.RunStep(t, p.Ctx, c, b.Recorder, fresh)
			statsExec(t, p, b, c, "RESET ROLE", 0)
		}
		replicationEmpty(t, p, b, held)
	})
}
