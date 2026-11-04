package reference_test

import (
	"context"
	"database/sql"
	"testing"

	"stuntgres/reference/fixture"
)

func TestReferencePGStatWALReceiverPrivileges(t *testing.T) {
	fixture.RoleReferenceCleanup(t, []string{"wal_receiver_reader", "wal_receiver_stats_reader", "wal_receiver_stats_group"})
	activityScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
		for _, query := range []string{
			"CREATE ROLE wal_receiver_reader",
			"CREATE ROLE wal_receiver_stats_reader INHERIT",
			"CREATE ROLE wal_receiver_stats_group",
			"GRANT pg_read_all_stats TO wal_receiver_stats_group",
			"GRANT wal_receiver_stats_group TO wal_receiver_stats_reader",
		} {
			statsExec(t, p, b, c, query, 0)
		}
		defer c.ExecContext(context.Background(), "RESET ROLE")
		held := walReceiverHeld(t, p, c, walReceiverSQL)
		defer held.Statement.Close()
		walReceiverEmpty(t, p, b, c)
		walReceiverEmpty(t, p, b, held)
		for _, role := range []string{"wal_receiver_reader", "wal_receiver_stats_reader"} {
			statsExec(t, p, b, c, "SET ROLE "+role, 0)
			// Empty results establish PUBLIC SELECT and absence under current
			// authority, not positive-row redaction: native has no WAL receiver.
			walReceiverEmpty(t, p, b, c)
			walReceiverEmpty(t, p, b, held)
			fresh := fixture.Prepared(walReceiverStep(t, b, walReceiverSQL, walReceiverAllColumns()))
			fixture.RunStep(t, p.Ctx, c, b.Recorder, fresh)
			statsExec(t, p, b, c, "RESET ROLE", 0)
		}
		walReceiverEmpty(t, p, b, held)
	})
}
