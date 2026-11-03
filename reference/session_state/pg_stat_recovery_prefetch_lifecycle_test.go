package reference_test

import (
	"context"
	"database/sql"
	"testing"

	"stuntgres/reference/fixture"
)

func TestReferencePGStatRecoveryPrefetchLifecycle(t *testing.T) {
	for _, mode := range []string{"none", "cache", "snapshot"} {
		t.Run(mode, func(t *testing.T) {
			activityScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
				peer := statActivityConn(t, p, b)
				defer c.ExecContext(context.Background(), "RESET ALL")
				statsExec(t, p, b, c, "SET stats_fetch_consistency="+mode, 0)
				statsExec(t, p, b, c, "CREATE TABLE prefetch_sentinel(v integer)", 0)
				statsExec(t, p, b, c, "INSERT INTO prefetch_sentinel VALUES(37)", 1)
				before := prefetchGet(t, p, b, c)
				held := prefetchHeld(t, p, c, prefetchSQL)
				defer held.Statement.Close()
				prefetchSame(t, before, prefetchGet(t, p, b, held))
				for _, query := range []string{"SET track_counts=off", "SET track_io_timing=on", "SET track_wal_io_timing=on", "SET track_counts=on", "SET track_io_timing=off", "SET track_wal_io_timing=off"} {
					statsExec(t, p, b, c, query, 0)
					prefetchSame(t, before, prefetchGet(t, p, b, held))
				}
				fixture.RunStep(t, p.Ctx, c, b.Recorder, fixture.Select("SHOW recovery_prefetch", []string{"recovery_prefetch"}, []string{"TEXT"}, [][]any{{"try"}}))
				statsExec(t, p, b, c, "UPDATE prefetch_sentinel SET v=41", 1)
				statsExec(t, p, b, c, "INSERT INTO prefetch_sentinel VALUES(43)", 1)
				statsExec(t, p, b, c, "DELETE FROM prefetch_sentinel WHERE v=43", 1)
				statsExec(t, p, b, c, "UPDATE prefetch_sentinel SET v=37", 1)
				prefetchSentinel(t, p, b, c)
				transient, err := b.DB.Conn(p.Ctx)
				if err != nil {
					t.Fatal(err)
				}
				if err := transient.Close(); err != nil {
					t.Fatal(err)
				}
				prefetchSame(t, before, prefetchGet(t, p, b, c))
				prefetchSame(t, before, prefetchGet(t, p, b, held))

				resetSQL := "SELECT pg_stat_reset_shared($1::text)"
				resetHeld := prefetchHeld(t, p, peer, resetSQL)
				defer resetHeld.Statement.Close()
				resetReplicationVoid(t, p, b, resetHeld, resetSQL, "pg_stat_reset_shared", "wal")
				prefetchSame(t, before, prefetchGet(t, p, b, held))
				for _, call := range []struct {
					query string
					args []any
				}{
					{resetSQL, []any{"recovery_prefetch"}},
					{resetSQL, []any{nil}},
					{"SELECT pg_stat_reset_shared()", nil},
				} {
					for _, prepared := range []bool{false, true} {
						statsExec(t, p, b, c, "BEGIN", 0)
						defer c.ExecContext(context.Background(), "ROLLBACK")
						statsNoTimestamp(t, p, c, nil)
						old := prefetchGet(t, p, b, held)
						statsNoTimestamp(t, p, c, nil)
						retained, err := held.QueryContext(p.Ctx, prefetchSQL)
						if err != nil {
							t.Fatal(err)
						}
						defer retained.Close()
						lower := prefetchClock(t, p, peer)
						var executor fixture.Executor = peer
						if prepared {
							if call.query == resetSQL {
								executor = resetHeld
							} else {
								h := prefetchHeld(t, p, peer, call.query)
								defer h.Statement.Close()
								executor = h
							}
						}
						resetReplicationVoid(t, p, b, executor, call.query, "pg_stat_reset_shared", call.args...)
						upper := prefetchClock(t, p, peer)
						prefetchSame(t, old, prefetchRead(t, retained, b.PG))
						current := prefetchGet(t, p, b, held)
						prefetchEvent(t, old, current, lower, upper)
						// Prefetch is current even in a cumulative snapshot transaction;
						// it must not create that snapshot as a side effect.
						statsNoTimestamp(t, p, c, nil)
						prefetchSame(t, current, prefetchGet(t, p, b, c))
						statsExec(t, p, b, c, "ROLLBACK", 0)
					}
				}
				old := prefetchGet(t, p, b, held)
				lower := prefetchClock(t, p, peer)
				statsExec(t, p, b, peer, "BEGIN", 0)
				defer peer.ExecContext(context.Background(), "ROLLBACK")
				resetReplicationVoid(t, p, b, resetHeld, resetSQL, "pg_stat_reset_shared", "recovery_prefetch")
				statsExec(t, p, b, peer, "ROLLBACK", 0)
				prefetchEvent(t, old, prefetchGet(t, p, b, held), lower, prefetchClock(t, p, peer))
				// Exec of the same SELECT follows the existing one-result expression contract.
				fixture.RunStep(t, p.Ctx, resetHeld, b.Recorder, fixture.Exec(resetSQL, 1, "recovery_prefetch"))
				prefetchGet(t, p, b, held)
				prefetchSentinel(t, p, b, c)
				if !b.PG {
					prefetchNativeControls(t, p, b, c, held)
				}
			})
		})
	}
}
