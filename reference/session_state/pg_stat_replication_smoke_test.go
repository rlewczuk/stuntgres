package reference_test

import (
	"context"
	"database/sql"
	"testing"

	"stuntgres/reference/fixture"
)

func TestReferencePGStatReplicationFeatureSmoke(t *testing.T) {
	fixture.RoleReferenceCleanup(t, []string{"replication_smoke_reader"})
	activityScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
		replicationEmpty(t, p, b, c)
		held := replicationHeld(t, p, c, replicationSQL)
		defer held.Statement.Close()
		replicationEmpty(t, p, b, held)
		peer := statActivityConn(t, p, b)
		statsExec(t, p, b, peer, "CREATE TABLE replication_sentinel(v integer)", 0)
		statsExec(t, p, b, peer, "INSERT INTO replication_sentinel VALUES(37)", 1)
		statsExec(t, p, b, peer, "SET application_name='not a WAL sender'", 0)
		replicationEmpty(t, p, b, held)
		statsExec(t, p, b, c, "CREATE ROLE replication_smoke_reader", 0)
		defer c.ExecContext(context.Background(), "RESET ROLE")
		statsExec(t, p, b, c, "SET ROLE replication_smoke_reader", 0)
		replicationEmpty(t, p, b, c)
		replicationEmpty(t, p, b, held)
		statsExec(t, p, b, c, "RESET ROLE", 0)
		step := fixture.ErrorDiagnostic("UPDATE pg_catalog.pg_stat_replication SET application_name='fiction' WHERE FALSE", replicationMutationDiagnostic("UPDATE"))
		step.ErrorStage = "execute"
		fixture.RunStep(t, p.Ctx, c, b.Recorder, step)
		replicationEmpty(t, p, b, held)
		replicationSentinel(t, p, b, c)
	})
}
