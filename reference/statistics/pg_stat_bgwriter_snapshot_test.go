package reference_test

import (
	"database/sql"
	"testing"

	"stuntgres/reference/fixture"
)

func bgwriterSnapshotTime(t *testing.T, p *fixture.Pair, c *sql.Conn) sql.NullTime {
	t.Helper()
	var at sql.NullTime
	if err := c.QueryRowContext(p.Ctx, "SELECT pg_stat_get_snapshot_timestamp()").Scan(&at); err != nil {
		t.Fatal(err)
	}
	return at
}

func TestReferencePGStatBGWriterSnapshot(t *testing.T) {
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
				held := bgwriterHeld(t, p, c, bgwriterSQL)
				defer held.Statement.Close()
				for _, terminal := range []string{"COMMIT", "ROLLBACK"} {
					fixture.StatisticsExec(t, p, b, c, "BEGIN", 0)
					defer c.ExecContext(p.Ctx, "ROLLBACK")
					fixture.StatisticsClearSnapshot(t, p, b, c)
					if at := bgwriterSnapshotTime(t, p, c); at.Valid {
						t.Fatal("prepare/clear must not capture", at)
					}
					// Prepare inside the transaction is a metadata-only operation.
					metadata := bgwriterHeld(t, p, c, bgwriterSQL)
					metadata.Statement.Close()
					if at := bgwriterSnapshotTime(t, p, c); at.Valid {
						t.Fatal("Describe populated snapshot", at)
					}
					a := bgwriterGet(t, p, b, held)
					cut := bgwriterSnapshotTime(t, p, c)
					if cut.Valid != (mode == "snapshot") {
						t.Fatal("snapshot timestamp mode", mode, cut)
					}
					lower := bgwriterClock(t, p, peer)
					bgwriterVoid(t, p, b, peer, "SELECT pg_stat_reset_shared('bgwriter')", "pg_stat_reset_shared")
					current := bgwriterGet(t, p, b, peer)
					bgwriterEvent(t, a, current, lower, bgwriterClock(t, p, peer))
					if !current.reset.After(a.reset) {
						t.Fatal("peer reset must distinguish reader cuts", a.reset, current.reset)
					}
					want := a
					if mode == "none" {
						want = current
					}
					if mode == "none" {
						bgwriterSameReset(t, want, bgwriterGet(t, p, b, held))
					} else {
						bgwriterSameFact(t, want, bgwriterGet(t, p, b, held))
					}
					if at := bgwriterSnapshotTime(t, p, c); at.Valid != cut.Valid || (at.Valid && !at.Time.Equal(cut.Time)) {
						t.Fatal("reset changed retained snapshot timestamp", cut, at)
					}
					fixture.StatisticsClearSnapshot(t, p, b, c)
					bgwriterSameReset(t, current, bgwriterGet(t, p, b, held))
					fixture.StatisticsExec(t, p, b, c, terminal, 0)
					bgwriterSameReset(t, current, bgwriterGet(t, p, b, held))
				}
				fixture.StatisticsExec(t, p, b, c, "BEGIN", 0)
				a := bgwriterGet(t, p, b, held)
				bgwriterVoid(t, p, b, peer, "SELECT pg_stat_reset_shared('bgwriter')", "pg_stat_reset_shared")
				current := bgwriterGet(t, p, b, peer)
				if !current.reset.After(a.reset) {
					t.Fatal("generation witness needs distinct reset instants")
				}
				fixture.StatisticsExec(t, p, b, c, "SET stats_fetch_consistency=none", 0)
				bgwriterSameReset(t, current, bgwriterGet(t, p, b, held))
				fixture.StatisticsExec(t, p, b, c, "COMMIT", 0)
			})
		})
	}
}