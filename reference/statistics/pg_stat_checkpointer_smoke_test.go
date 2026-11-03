package reference_test

import (
	"bytes"
	"context"
	"database/sql"
	"testing"

	"stuntgres"
	"stuntgres/reference/fixture"
)

func TestReferencePGStatCheckpointerFeatureSmoke(t *testing.T) {
	fixture.RoleReferenceCleanup(t, []string{"checkpointer_smoke_reader"})
	names := []string{"checkpointer_smoke_peer", "checkpointer_smoke_late"}
	fixture.DatabaseReferenceCleanup(t, &names)
	fixture.StatisticsScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
		restoreACLs := checkpointerRestoreACLs(t, p, b, c)
		fixture.StatisticsExec(t, p, b, c, "SET stats_fetch_consistency=none", 0)
		a := checkpointerGet(t, p, b, c)
		held := checkpointerHeld(t, p, c, checkpointerSQL)
		defer held.Statement.Close()
		checkpointerSameReset(t, a, checkpointerGet(t, p, b, held))
		fixture.StatisticsExec(t, p, b, c, "CREATE TABLE checkpointer_sentinel(v integer)", 0)
		fixture.StatisticsExec(t, p, b, c, "INSERT INTO checkpointer_sentinel VALUES(37)", 1)
		checkpointerSentinel(t, p, b, c)
		checkpointerSameReset(t, a, checkpointerGet(t, p, b, held))
		fixture.StatisticsExec(t, p, b, c, "CREATE DATABASE "+names[0], 0)
		pool, _, _ := fixture.DatabaseScenarioPool(t, p, b.PG, names[0], "")
		peer, err := pool.Conn(p.Ctx)
		if err != nil {
			t.Fatal(err)
		}
		defer peer.Close()
		fixture.StatisticsExec(t, p, b, peer, "SET stats_fetch_consistency=none", 0)
		const resetSQL = "SELECT pg_catalog.pg_stat_reset_shared($1::text)"
		reset := checkpointerHeld(t, p, peer, resetSQL)
		defer reset.Statement.Close()
		old, err := held.QueryContext(p.Ctx, checkpointerSQL)
		if err != nil {
			t.Fatal(err)
		}
		defer old.Close()
		lower := checkpointerClock(t, p, peer)
		checkpointerVoid(t, p, b, reset, resetSQL, "pg_stat_reset_shared", "checkpointer")
		checkpointerSameReset(t, a, checkpointerRead(t, old, b.PG))
		current := checkpointerGet(t, p, b, held)
		checkpointerEvent(t, a, current, lower, checkpointerClock(t, p, peer))
		fixture.StatisticsExec(t, p, b, c, "SET stats_fetch_consistency=snapshot", 0)
		fixture.StatisticsExec(t, p, b, c, "BEGIN", 0)
		defer c.ExecContext(p.Ctx, "ROLLBACK")
		emptyCut := checkpointerSnapshotTime(t, p, c)
		if emptyCut.Valid {
			t.Fatal("BEGIN unexpectedly initialized cumulative snapshot", emptyCut)
		}
		fixture.RunStep(t, p.Ctx, c, b.Recorder, fixture.Select("SELECT count(num_timed) FROM pg_catalog.pg_stat_checkpointer WHERE FALSE", []string{"count"}, []string{"INT8"}, [][]any{{int64(0)}}))
		checkpointerSnapshotUnchanged(t, p, c, emptyCut)
		a = checkpointerGet(t, p, b, held)
		lower = checkpointerClock(t, p, peer)
		checkpointerVoid(t, p, b, reset, resetSQL, "pg_stat_reset_shared", "checkpointer")
		current = checkpointerGet(t, p, b, peer)
		checkpointerEvent(t, a, current, lower, checkpointerClock(t, p, peer))
		if !current.reset.After(a.reset) {
			t.Fatal("reset did not distinguish snapshot cut")
		}
		checkpointerSameFact(t, a, checkpointerGet(t, p, b, held))
		fixture.StatisticsClearSnapshot(t, p, b, c)
		checkpointerSameReset(t, current, checkpointerGet(t, p, b, held))
		fixture.StatisticsExec(t, p, b, c, "COMMIT", 0)
		step := fixture.ErrorDiagnostic("UPDATE pg_catalog.pg_stat_checkpointer SET num_timed=1 WHERE FALSE", checkpointerMutationDiagnostic("UPDATE"))
		step.ErrorStage = "execute"
		fixture.RunStep(t, p.Ctx, c, b.Recorder, step)
		checkpointerSameReset(t, current, checkpointerGet(t, p, b, held))
		checkpointerSentinel(t, p, b, c)
		fixture.StatisticsExec(t, p, b, c, "CREATE ROLE checkpointer_smoke_reader", 0)
		defer func() {
			defer restoreACLs()
			ctx, cancel := context.WithTimeout(context.Background(), fixture.Liveness)
			defer cancel()
			for _, query := range []string{"ROLLBACK", "RESET ROLE", "GRANT EXECUTE ON FUNCTION pg_catalog.pg_stat_get_checkpointer_num_timed() TO PUBLIC"} {
				if _, err := c.ExecContext(ctx, query); err != nil {
					t.Error(query, err)
				}
			}
		}()
		fixture.StatisticsExec(t, p, b, c, "REVOKE EXECUTE ON FUNCTION pg_catalog.pg_stat_get_checkpointer_num_timed() FROM PUBLIC", 0)
		fixture.StatisticsExec(t, p, b, c, "SET ROLE checkpointer_smoke_reader", 0)
		fixture.StatisticsExec(t, p, b, c, "BEGIN", 0)
		fixture.RunStep(t, p.Ctx, c, b.Recorder, fixture.Select("SELECT stats_reset FROM pg_stat_checkpointer", []string{"stats_reset"}, []string{"TIMESTAMPTZ"}, [][]any{{fixture.Normalize(current.reset)}}))
		cut := checkpointerSnapshotTime(t, p, c)
		if !cut.Valid {
			t.Fatal("permitted sibling did not initialize smoke snapshot")
		}
		fixture.StatisticsExec(t, p, b, c, "SAVEPOINT smoke_denied", 0)
		checkpointerQueryDenied(t, p, b, c, "SELECT num_timed FROM pg_stat_checkpointer", "permission denied for function pg_stat_get_checkpointer_num_timed")
		fixture.StatisticsExec(t, p, b, c, "ROLLBACK TO SAVEPOINT smoke_denied", 0)
		checkpointerSnapshotUnchanged(t, p, c, cut)
		fixture.StatisticsExec(t, p, b, c, "ROLLBACK", 0)
		fixture.StatisticsExec(t, p, b, c, "RESET ROLE", 0)
		fixture.StatisticsExec(t, p, b, c, "GRANT EXECUTE ON FUNCTION pg_catalog.pg_stat_get_checkpointer_num_timed() TO PUBLIC", 0)
		fixture.StatisticsExec(t, p, b, c, "SET ROLE checkpointer_smoke_reader", 0)
		fixture.RunStep(t, p.Ctx, c, b.Recorder, fixture.Select("SELECT num_timed IS NOT NULL AS present FROM pg_stat_checkpointer", []string{"present"}, []string{"BOOL"}, [][]any{{true}}))
		fixture.StatisticsExec(t, p, b, c, "RESET ROLE", 0)
		fixture.StatisticsExec(t, p, b, c, "CREATE DATABASE "+names[1], 0)
		latePool, _, _ := fixture.DatabaseScenarioPool(t, p, b.PG, names[1], "")
		late, err := latePool.Conn(p.Ctx)
		if err != nil {
			t.Fatal(err)
		}
		defer late.Close()
		checkpointerSameReset(t, current, checkpointerGet(t, p, b, late))
		checkpointerSameReset(t, current, checkpointerGet(t, p, b, peer))
		if !b.PG {
			var dump bytes.Buffer
			if err := stuntgres.Dump(p.Ctx, p.Native, &dump); err != nil {
				t.Fatal(err)
			}
			if err := stuntgres.Reset(p.Ctx, p.Native); err != nil {
				t.Fatal(err)
			}
			checkpointerSameReset(t, current, checkpointerGet(t, p, b, held))
			if err := stuntgres.Restore(p.Ctx, p.Native, bytes.NewReader(dump.Bytes())); err != nil {
				t.Fatal(err)
			}
			checkpointerSentinel(t, p, b, c)
			for _, connection := range []*sql.Conn{c, peer, late} {
				checkpointerSameReset(t, current, checkpointerGet(t, p, b, connection))
			}
		}
	})
}
