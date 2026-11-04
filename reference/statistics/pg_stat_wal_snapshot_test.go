package reference_test

import (
	"database/sql"
	"testing"

	"stuntgres/reference/fixture"
)

func TestReferencePGStatWALSnapshot(t *testing.T) {
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
				held := archiverHeld(t, p, c, walSQL)
				defer held.Statement.Close()
				for _, terminal := range []string{"COMMIT", "ROLLBACK"} {
					fixture.StatisticsExec(t, p, b, c, "BEGIN", 0)
					defer c.ExecContext(p.Ctx, "ROLLBACK")
					fixture.StatisticsClearSnapshot(t, p, b, c)
					if at := archiverSnapshotTime(t, p, c); at.Valid {
						t.Fatal("prepare/clear must not capture", at)
					}
					// Prepare inside the transaction is a metadata-only operation.
					metadata := archiverHeld(t, p, c, walSQL)
					metadata.Statement.Close()
					if at := archiverSnapshotTime(t, p, c); at.Valid {
						t.Fatal("Describe populated snapshot", at)
					}
					a := walGet(t, p, b, held)
					cut := archiverSnapshotTime(t, p, c)
					if cut.Valid != (mode == "snapshot") {
						t.Fatal("snapshot timestamp mode", mode, cut)
					}
					lower := archiverClock(t, p, peer)
					archiverVoid(t, p, b, peer, "SELECT pg_stat_reset_shared('wal')", "pg_stat_reset_shared")
					current := walGet(t, p, b, peer)
					walEvent(t, a, current, lower, archiverClock(t, p, peer))
					if !current.reset.After(a.reset) {
						t.Fatal("peer reset must distinguish reader cuts", a.reset, current.reset)
					}
					want := a
					if mode == "none" {
						want = current
					}
					walSame(t, want, walGet(t, p, b, held))
					if at := archiverSnapshotTime(t, p, c); at.Valid != cut.Valid || (at.Valid && !at.Time.Equal(cut.Time)) {
						t.Fatal("reset changed retained snapshot timestamp", cut, at)
					}
					fixture.StatisticsClearSnapshot(t, p, b, c)
					walSame(t, current, walGet(t, p, b, held))
					fixture.StatisticsExec(t, p, b, c, terminal, 0)
					walSame(t, current, walGet(t, p, b, held))
				}
				fixture.StatisticsExec(t, p, b, c, "BEGIN", 0)
				a := walGet(t, p, b, held)
				archiverVoid(t, p, b, peer, "SELECT pg_stat_reset_shared('wal')", "pg_stat_reset_shared")
				current := walGet(t, p, b, peer)
				if !current.reset.After(a.reset) {
					t.Fatal("generation witness needs distinct reset instants")
				}
				fixture.StatisticsExec(t, p, b, c, "SET stats_fetch_consistency=none", 0)
				walSame(t, current, walGet(t, p, b, held))
				fixture.StatisticsExec(t, p, b, c, "COMMIT", 0)
				for _, setting := range []string{"SET track_counts=off", "SET track_io_timing=on", "SET track_wal_io_timing=on", "SET track_counts=on", "SET track_io_timing=off", "SET track_wal_io_timing=off"} {
					fixture.StatisticsExec(t, p, b, c, setting, 0)
					walSame(t, current, walGet(t, p, b, held))
				}
			})
		})
	}
}
