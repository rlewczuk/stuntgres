package reference_test

import (
	"context"
	"database/sql"
	"testing"

	"stuntgres/reference/fixture"
)

func TestReferencePGStatWALReceiverFeatureSmoke(t *testing.T) {
	fixture.RoleReferenceCleanup(t, []string{"wal_receiver_smoke_reader"})
	activityScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
		walReceiverEmpty(t, p, b, c)
		const projection = "SELECT pid,receive_start_lsn,last_msg_receipt_time,sender_host,sender_port,conninfo FROM pg_catalog.pg_stat_wal_receiver"
		held := walReceiverHeld(t, p, c, projection)
		defer held.Statement.Close()
		step := walReceiverStep(t, b, projection, []int{0, 2, 8, 12, 13, 14})
		fixture.RunStep(t, p.Ctx, held, b.Recorder, step)
		peer := statActivityConn(t, p, b)
		statsExec(t, p, b, peer, "CREATE TABLE wal_receiver_sentinel(v integer)", 0)
		statsExec(t, p, b, peer, "INSERT INTO wal_receiver_sentinel VALUES(37)", 1)
		statsExec(t, p, b, peer, "SET application_name='not a WAL receiver'", 0)
		fixture.RunStep(t, p.Ctx, held, b.Recorder, step)
		walReceiverEmpty(t, p, b, c)
		statsExec(t, p, b, c, "CREATE ROLE wal_receiver_smoke_reader", 0)
		defer c.ExecContext(context.Background(), "RESET ROLE")
		statsExec(t, p, b, c, "SET ROLE wal_receiver_smoke_reader", 0)
		walReceiverEmpty(t, p, b, c)
		fixture.RunStep(t, p.Ctx, held, b.Recorder, step)
		statsExec(t, p, b, c, "RESET ROLE", 0)
		rejected := fixture.ErrorDiagnostic("UPDATE pg_catalog.pg_stat_wal_receiver SET status='fiction' WHERE FALSE", walReceiverMutationDiagnostic("UPDATE"))
		rejected.ErrorStage = "execute"
		fixture.RunStep(t, p.Ctx, c, b.Recorder, rejected)
		fixture.RunStep(t, p.Ctx, held, b.Recorder, step)
		walReceiverEmpty(t, p, b, c)
		walReceiverSentinel(t, p, b, c)
	})
}
