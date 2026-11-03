package reference_test

import (
	"context"
	"database/sql"
	"testing"

	"stuntgres/reference/fixture"
)

func slruConsistency(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn, mode string) {
	t.Helper()
	peer := statActivityConn(t, p, b)
	statsExec(t, p, b, c, "SET stats_fetch_consistency="+mode, 0)
	statsExec(t, p, b, c, "BEGIN", 0)
	defer c.ExecContext(context.Background(), "ROLLBACK")
	held := slruHeld(t, p, c, slruSQL); defer held.Statement.Close()
	// Prepare/Describe must not create the cumulative snapshot.
	statsNoTimestamp(t, p, c, nil)
	old := slruGet(t, p, b, held)
	var stamp sql.NullTime
	if err := c.QueryRowContext(p.Ctx, "SELECT pg_stat_get_snapshot_timestamp()").Scan(&stamp); err != nil { t.Fatal(err) }
	if stamp.Valid != (mode == "snapshot") { t.Fatal("mode-correct snapshot admission", mode, stamp) }
	retained, err := held.QueryContext(p.Ctx, slruSQL); if err != nil { t.Fatal(err) }; defer retained.Close()
	lower := slruDistinctClock(t, p, peer, old.rows["other"].reset)
	resetReplicationVoid(t, p, b, peer, "SELECT pg_stat_reset_slru('other')", "pg_stat_reset_slru")
	upper := slruClock(t, p, peer)
	slruSame(t, old, slruRead(t, retained, b.PG))
	current := slruGet(t, p, b, held)
	if mode == "none" { slruEvent(t, old, current, lower, upper) } else { slruSame(t, old, current) }
	var retainedStamp sql.NullTime
	if err := c.QueryRowContext(p.Ctx, "SELECT pg_stat_get_snapshot_timestamp()").Scan(&retainedStamp); err != nil || retainedStamp.Valid != stamp.Valid || (stamp.Valid && !retainedStamp.Time.Equal(stamp.Time)) { t.Fatal("retained snapshot timestamp", retainedStamp, stamp, err) }
	resetReplicationVoid(t, p, b, c, "SELECT pg_stat_clear_snapshot()", "pg_stat_clear_snapshot")
	statsNoTimestamp(t, p, c, nil)
	slruEvent(t, old, slruGet(t, p, b, held), lower, upper)
	if mode == "snapshot" {
		var fresh sql.NullTime
		if err := c.QueryRowContext(p.Ctx, "SELECT pg_stat_get_snapshot_timestamp()").Scan(&fresh); err != nil || !fresh.Valid || !fresh.Time.After(stamp.Time) { t.Fatal("new snapshot timestamp", fresh, stamp, err) }
	}
	statsExec(t, p, b, c, "ROLLBACK", 0)
}

func TestReferencePGStatSLRULifecycle(t *testing.T) {
	activityScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
		defer c.ExecContext(context.Background(), "RESET ALL")
		for _, mode := range []string{"none", "cache", "snapshot"} { slruConsistency(t, p, b, c, mode) }
		statsExec(t, p, b, c, "SET stats_fetch_consistency=none", 0)
		statsExec(t, p, b, c, "CREATE TABLE slru_sentinel(v integer)", 0)
		statsExec(t, p, b, c, "INSERT INTO slru_sentinel VALUES(37)", 1)
		before := slruGet(t, p, b, c)
		// Database-local reset is outside this view's embedded capability:
		// assert the native boundary independently, not as PostgreSQL parity.
		if b.PG {
			resetReplicationVoid(t, p, b, c, "SELECT pg_stat_reset()", "pg_stat_reset")
		} else {
			activityError(t, p, c, "SELECT pg_stat_reset()", activityDiagnostic("0A000", "functions are unsupported", 8))
		}
		slruSame(t, before, slruGet(t, p, b, c))
		resetReplicationVoid(t, p, b, c, "SELECT pg_stat_reset_shared('wal')", "pg_stat_reset_shared")
		slruSame(t, before, slruGet(t, p, b, c))
		for _, call := range []struct{ query, function string; args []any }{
			{"SELECT pg_stat_reset_slru($1::text)", "pg_stat_reset_slru", []any{"other"}},
			{"SELECT pg_stat_reset_slru($1::text)", "pg_stat_reset_slru", []any{"unknown"}},
			{"SELECT pg_stat_reset_slru($1::text)", "pg_stat_reset_slru", []any{""}},
			{"SELECT pg_stat_reset_slru($1::text)", "pg_stat_reset_slru", []any{"TRANSACTION"}},
			{"SELECT pg_stat_reset_slru($1::text)", "pg_stat_reset_slru", []any{"transaction "}},
			{"SELECT pg_stat_reset_slru($1::text)", "pg_stat_reset_slru", []any{nil}},
			{"SELECT pg_stat_reset_slru()", "pg_stat_reset_slru", nil},
			{"SELECT pg_stat_reset_shared('slru')", "pg_stat_reset_shared", nil},
			{"SELECT pg_stat_reset_shared(NULL)", "pg_stat_reset_shared", nil},
			{"SELECT pg_stat_reset_shared()", "pg_stat_reset_shared", nil},
		} {
			for _, prepared := range []bool{false,true} {
				old := slruGet(t, p, b, c)
				lower := slruDistinctClock(t, p, c, old.rows["other"].reset)
				var executor fixture.Executor = c
				if prepared { h := slruHeld(t, p, c, call.query); defer h.Statement.Close(); executor = h }
				resetReplicationVoid(t, p, b, executor, call.query, call.function, call.args...)
				fresh := slruGet(t, p, b, c)
				slruEvent(t, old, fresh, lower, slruClock(t, p, c))
				if len(call.args) == 1 && call.args[0] != nil {
					for name, row := range old.rows { if name != "other" && !fresh.rows[name].reset.Equal(row.reset) { t.Fatal("selected reset changed peer reset metadata", name) } }
				} else {
					for name, row := range fresh.rows { if !row.reset.Equal(fresh.rows["other"].reset) { t.Fatal("all reset differs across categories", name, row) } }
				}
			}
		}
		old := slruGet(t, p, b, c)
		lower := slruDistinctClock(t, p, c, old.rows["other"].reset)
		statsExec(t, p, b, c, "BEGIN", 0)
		resetReplicationVoid(t, p, b, c, "SELECT pg_stat_reset_slru('other')", "pg_stat_reset_slru")
		statsExec(t, p, b, c, "ROLLBACK", 0)
		slruEvent(t, old, slruGet(t, p, b, c), lower, slruClock(t, p, c))
		fixture.RunStep(t, p.Ctx, c, b.Recorder, fixture.Exec("SELECT pg_stat_reset_slru('other')", 1))
		slruSentinel(t, p, b, c)
	})
}
