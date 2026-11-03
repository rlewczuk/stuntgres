package reference_test

import (
	"database/sql"
	"testing"

	"stuntgres/reference/fixture"
)

func archiverSnapshotTime(t *testing.T, p *fixture.Pair, c *sql.Conn) sql.NullTime {
	t.Helper()
	var at sql.NullTime
	if err := c.QueryRowContext(p.Ctx, "SELECT pg_stat_get_snapshot_timestamp()").Scan(&at); err != nil {
		t.Fatal(err)
	}
	return at
}

func TestReferencePGStatArchiverSnapshots(t *testing.T) {
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
				held := archiverHeld(t, p, c, archiverSQL)
				defer held.Statement.Close()
				for _, terminal := range []string{"COMMIT", "ROLLBACK"} {
					fixture.StatisticsExec(t, p, b, c, "BEGIN", 0)
					defer c.ExecContext(p.Ctx, "ROLLBACK")
					fixture.StatisticsClearSnapshot(t, p, b, c)
					if at := archiverSnapshotTime(t, p, c); at.Valid {
						t.Fatal("prepare/clear must not capture", at)
					}
					// Prepare inside the transaction is a metadata-only operation.
					metadata := archiverHeld(t, p, c, archiverSQL)
					metadata.Statement.Close()
					if at := archiverSnapshotTime(t, p, c); at.Valid {
						t.Fatal("Describe populated snapshot", at)
					}
					a := archiverGet(t, p, b, held)
					cut := archiverSnapshotTime(t, p, c)
					if cut.Valid != (mode == "snapshot") {
						t.Fatal("snapshot timestamp mode", mode, cut)
					}
					lower := archiverClock(t, p, peer)
					archiverVoid(t, p, b, peer, "SELECT pg_stat_reset_shared('archiver')", "pg_stat_reset_shared")
					current := archiverGet(t, p, b, peer)
					archiverEvent(t, a, current, lower, archiverClock(t, p, peer))
					if !current.reset.After(a.reset) {
						t.Fatal("peer reset must distinguish reader cuts", a.reset, current.reset)
					}
					want := a
					if mode == "none" {
						want = current
					}
					archiverSame(t, want, archiverGet(t, p, b, held))
					if at := archiverSnapshotTime(t, p, c); at.Valid != cut.Valid || (at.Valid && !at.Time.Equal(cut.Time)) {
						t.Fatal("reset changed retained snapshot timestamp", cut, at)
					}
					fixture.StatisticsClearSnapshot(t, p, b, c)
					archiverSame(t, current, archiverGet(t, p, b, held))
					fixture.StatisticsExec(t, p, b, c, terminal, 0)
					archiverSame(t, current, archiverGet(t, p, b, held))
				}
				fixture.StatisticsExec(t, p, b, c, "BEGIN", 0)
				a := archiverGet(t, p, b, held)
				archiverVoid(t, p, b, peer, "SELECT pg_stat_reset_shared('archiver')", "pg_stat_reset_shared")
				current := archiverGet(t, p, b, peer)
				if !current.reset.After(a.reset) {
					t.Fatal("generation witness needs distinct reset instants")
				}
				fixture.StatisticsExec(t, p, b, c, "SET stats_fetch_consistency=none", 0)
				archiverSame(t, current, archiverGet(t, p, b, held))
				fixture.StatisticsExec(t, p, b, c, "COMMIT", 0)
			})
		})
	}
}
