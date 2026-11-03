package reference_test

import (
	"database/sql"
	"testing"

	"stuntgres/reference/fixture"
)

func checkpointerSnapshotTime(t *testing.T, p *fixture.Pair, c *sql.Conn) sql.NullTime {
	t.Helper()
	var at sql.NullTime
	if err := c.QueryRowContext(p.Ctx, "SELECT pg_stat_get_snapshot_timestamp()").Scan(&at); err != nil {
		t.Fatal(err)
	}
	return at
}

func TestReferencePGStatCheckpointerSnapshot(t *testing.T) {
	for _, mode := range []string{"none", "cache", "snapshot"} {
		t.Run(mode, func(t *testing.T) {
			fixture.StatisticsScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
				peer, err := b.DB.Conn(p.Ctx)
				if err != nil {
					t.Fatal(err)
				}
				defer peer.Close()
				fixture.StatisticsExec(t, p, b, peer, "SET stats_fetch_consistency=none", 0)
				fixture.StatisticsExec(t, p, b, c, "SET stats_fetch_consistency="+mode, 0)
				held := checkpointerHeld(t, p, c, checkpointerSQL)
				defer held.Statement.Close()
				if mode == "snapshot" {
					// Another cumulative family must own the same all-kind cut,
					// even before checkpointer itself has been requested.
					fixture.StatisticsExec(t, p, b, c, "BEGIN", 0)
					fixture.StatisticsClearSnapshot(t, p, b, c)
					before := checkpointerGet(t, p, b, peer)
					bgwriterGet(t, p, b, c)
					cut := checkpointerSnapshotTime(t, p, c)
					if !cut.Valid {
						t.Fatal("first other family did not initialize snapshot")
					}
					checkpointerVoid(t, p, b, peer, "SELECT pg_stat_reset_shared('checkpointer')", "pg_stat_reset_shared")
					current := checkpointerGet(t, p, b, peer)
					if !current.reset.After(before.reset) {
						t.Fatal("cross-family cut lacks distinct reset")
					}
					checkpointerSameReset(t, before, checkpointerGet(t, p, b, held))
					checkpointerSnapshotUnchanged(t, p, c, cut)
					fixture.StatisticsClearSnapshot(t, p, b, c)
					checkpointerSameReset(t, current, checkpointerGet(t, p, b, held))
					fixture.StatisticsExec(t, p, b, c, "ROLLBACK", 0)
				}
				for _, terminal := range []string{"COMMIT", "ROLLBACK"} {
					fixture.StatisticsExec(t, p, b, c, "BEGIN", 0)
					defer c.ExecContext(p.Ctx, "ROLLBACK")
					fixture.StatisticsClearSnapshot(t, p, b, c)
					if at := checkpointerSnapshotTime(t, p, c); at.Valid {
						t.Fatal("prepare/clear must not capture", at)
					}
					// Prepare inside the transaction is a metadata-only operation.
					metadata := checkpointerHeld(t, p, c, checkpointerSQL)
					metadata.Statement.Close()
					if at := checkpointerSnapshotTime(t, p, c); at.Valid {
						t.Fatal("Describe populated snapshot", at)
					}
					a := checkpointerGet(t, p, b, held)
					cut := checkpointerSnapshotTime(t, p, c)
					if cut.Valid != (mode == "snapshot") {
						t.Fatal("snapshot timestamp mode", mode, cut)
					}
					lower := checkpointerClock(t, p, peer)
					checkpointerVoid(t, p, b, peer, "SELECT pg_stat_reset_shared('checkpointer')", "pg_stat_reset_shared")
					current := checkpointerGet(t, p, b, peer)
					checkpointerEvent(t, a, current, lower, checkpointerClock(t, p, peer))
					if !current.reset.After(a.reset) {
						t.Fatal("peer reset must distinguish reader cuts", a.reset, current.reset)
					}
					want := a
					if mode == "none" {
						want = current
					}
					if mode == "none" {
						checkpointerSameReset(t, want, checkpointerGet(t, p, b, held))
					} else {
						checkpointerSameFact(t, want, checkpointerGet(t, p, b, held))
					}
					if at := checkpointerSnapshotTime(t, p, c); at.Valid != cut.Valid || (at.Valid && !at.Time.Equal(cut.Time)) {
						t.Fatal("reset changed retained snapshot timestamp", cut, at)
					}
					fixture.StatisticsClearSnapshot(t, p, b, c)
					checkpointerSameReset(t, current, checkpointerGet(t, p, b, held))
					fixture.StatisticsExec(t, p, b, c, terminal, 0)
					checkpointerSameReset(t, current, checkpointerGet(t, p, b, held))
				}
				fixture.StatisticsExec(t, p, b, c, "BEGIN", 0)
				a := checkpointerGet(t, p, b, held)
				checkpointerVoid(t, p, b, peer, "SELECT pg_stat_reset_shared('checkpointer')", "pg_stat_reset_shared")
				current := checkpointerGet(t, p, b, peer)
				if !current.reset.After(a.reset) {
					t.Fatal("generation witness needs distinct reset instants")
				}
				fixture.StatisticsExec(t, p, b, c, "SET stats_fetch_consistency=none", 0)
				checkpointerSameReset(t, current, checkpointerGet(t, p, b, held))
				fixture.StatisticsExec(t, p, b, c, "COMMIT", 0)
			})
		})
	}
}
