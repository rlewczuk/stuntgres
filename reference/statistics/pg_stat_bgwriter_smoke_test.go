package reference_test

import (
	"bytes"
	"context"
	"database/sql"
	"testing"

	"stuntgres"
	"stuntgres/reference/fixture"
)

func TestReferencePGStatBGWriterFeatureSmoke(t *testing.T) {
	fixture.RoleReferenceCleanup(t, []string{"bgwriter_smoke_reader"})
	names := []string{"bgwriter_smoke_peer", "bgwriter_smoke_late"}
	fixture.DatabaseReferenceCleanup(t, &names)
	fixture.StatisticsScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
		restoreACLs := bgwriterRestoreACLs(t, p, b, c)
		fixture.StatisticsExec(t, p, b, c, "SET stats_fetch_consistency=none", 0)
		a := bgwriterGet(t, p, b, c)
		held := bgwriterHeld(t, p, c, bgwriterSQL)
		defer held.Statement.Close()
		bgwriterSameReset(t, a, bgwriterGet(t, p, b, held))
		fixture.StatisticsExec(t, p, b, c, "CREATE TABLE bgwriter_sentinel(v integer)", 0)
		fixture.StatisticsExec(t, p, b, c, "INSERT INTO bgwriter_sentinel VALUES(37)", 1)
		bgwriterSentinel(t, p, b, c)
		bgwriterSameReset(t, a, bgwriterGet(t, p, b, held))
		fixture.StatisticsExec(t, p, b, c, "CREATE DATABASE "+names[0], 0)
		pool, _, _ := fixture.DatabaseScenarioPool(t, p, b.PG, names[0], "")
		peer, err := pool.Conn(p.Ctx)
		if err != nil {
			t.Fatal(err)
		}
		defer peer.Close()
		fixture.StatisticsExec(t, p, b, peer, "SET stats_fetch_consistency=none", 0)
		const resetSQL = "SELECT pg_catalog.pg_stat_reset_shared($1::text)"
		reset := bgwriterHeld(t, p, peer, resetSQL)
		defer reset.Statement.Close()
		old, err := held.QueryContext(p.Ctx, bgwriterSQL)
		if err != nil {
			t.Fatal(err)
		}
		defer old.Close()
		lower := bgwriterClock(t, p, peer)
		bgwriterVoid(t, p, b, reset, resetSQL, "pg_stat_reset_shared", "bgwriter")
		bgwriterSameReset(t, a, bgwriterRead(t, old, b.PG))
		current := bgwriterGet(t, p, b, held)
		bgwriterEvent(t, a, current, lower, bgwriterClock(t, p, peer))
		fixture.StatisticsExec(t, p, b, c, "SET stats_fetch_consistency=snapshot", 0)
		fixture.StatisticsExec(t, p, b, c, "BEGIN", 0)
		defer c.ExecContext(p.Ctx, "ROLLBACK")
		emptyCut := bgwriterSnapshotTime(t, p, c)
		if emptyCut.Valid {
			t.Fatal("BEGIN unexpectedly initialized cumulative snapshot", emptyCut)
		}
		fixture.RunStep(t, p.Ctx, c, b.Recorder, fixture.Select("SELECT count(buffers_clean) FROM pg_catalog.pg_stat_bgwriter WHERE FALSE", []string{"count"}, []string{"INT8"}, [][]any{{int64(0)}}))
		bgwriterSnapshotUnchanged(t, p, c, emptyCut)
		a = bgwriterGet(t, p, b, held)
		lower = bgwriterClock(t, p, peer)
		bgwriterVoid(t, p, b, reset, resetSQL, "pg_stat_reset_shared", "bgwriter")
		current = bgwriterGet(t, p, b, peer)
		bgwriterEvent(t, a, current, lower, bgwriterClock(t, p, peer))
		if !current.reset.After(a.reset) {
			t.Fatal("reset did not distinguish snapshot cut")
		}
		bgwriterSameFact(t, a, bgwriterGet(t, p, b, held))
		fixture.StatisticsClearSnapshot(t, p, b, c)
		bgwriterSameReset(t, current, bgwriterGet(t, p, b, held))
		fixture.StatisticsExec(t, p, b, c, "COMMIT", 0)
		step := fixture.ErrorDiagnostic("UPDATE pg_catalog.pg_stat_bgwriter SET buffers_clean=1 WHERE FALSE", bgwriterMutationDiagnostic("UPDATE"))
		step.ErrorStage = "execute"
		fixture.RunStep(t, p.Ctx, c, b.Recorder, step)
		bgwriterSameReset(t, current, bgwriterGet(t, p, b, held))
		bgwriterSentinel(t, p, b, c)
		fixture.StatisticsExec(t, p, b, c, "CREATE ROLE bgwriter_smoke_reader", 0)
		defer func() {
			defer restoreACLs()
			ctx, cancel := context.WithTimeout(context.Background(), fixture.Liveness)
			defer cancel()
			for _, query := range []string{"ROLLBACK", "RESET ROLE", "GRANT EXECUTE ON FUNCTION pg_catalog.pg_stat_get_bgwriter_buf_written_clean() TO PUBLIC"} {
				if _, err := c.ExecContext(ctx, query); err != nil {
					t.Error(query, err)
				}
			}
		}()
		fixture.StatisticsExec(t, p, b, c, "REVOKE EXECUTE ON FUNCTION pg_catalog.pg_stat_get_bgwriter_buf_written_clean() FROM PUBLIC", 0)
		fixture.StatisticsExec(t, p, b, c, "SET ROLE bgwriter_smoke_reader", 0)
		fixture.RunStep(t, p.Ctx, c, b.Recorder, fixture.Select("SELECT stats_reset FROM pg_stat_bgwriter", []string{"stats_reset"}, []string{"TIMESTAMPTZ"}, [][]any{{fixture.Normalize(current.reset)}}))
		bgwriterQueryDenied(t, p, b, c, "SELECT buffers_clean FROM pg_stat_bgwriter", "permission denied for function pg_stat_get_bgwriter_buf_written_clean")
		fixture.StatisticsExec(t, p, b, c, "RESET ROLE", 0)
		fixture.StatisticsExec(t, p, b, c, "GRANT EXECUTE ON FUNCTION pg_catalog.pg_stat_get_bgwriter_buf_written_clean() TO PUBLIC", 0)
		fixture.StatisticsExec(t, p, b, c, "CREATE DATABASE "+names[1], 0)
		latePool, _, _ := fixture.DatabaseScenarioPool(t, p, b.PG, names[1], "")
		late, err := latePool.Conn(p.Ctx)
		if err != nil {
			t.Fatal(err)
		}
		defer late.Close()
		bgwriterSameReset(t, current, bgwriterGet(t, p, b, late))
		bgwriterSameReset(t, current, bgwriterGet(t, p, b, peer))
		if !b.PG {
			var dump bytes.Buffer
			if err := stuntgres.Dump(p.Ctx, p.Native, &dump); err != nil {
				t.Fatal(err)
			}
			if err := stuntgres.Reset(p.Ctx, p.Native); err != nil {
				t.Fatal(err)
			}
			bgwriterSameReset(t, current, bgwriterGet(t, p, b, held))
			if err := stuntgres.Restore(p.Ctx, p.Native, bytes.NewReader(dump.Bytes())); err != nil {
				t.Fatal(err)
			}
			bgwriterSentinel(t, p, b, c)
			for _, connection := range []*sql.Conn{c, peer, late} {
				bgwriterSameReset(t, current, bgwriterGet(t, p, b, connection))
			}
		}
	})
}