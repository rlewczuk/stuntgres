package reference_test

import (
	"context"
	"database/sql"
	"testing"

	"stuntgres/reference/fixture"
)

func TestReferenceStatsXactBlocksCacheAndTracking(t *testing.T) {
	for _, mode := range []string{"none", "cache", "snapshot"} {
		t.Run(mode, func(t *testing.T) {
			activityScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
				statsCreate(t, p, b, c, "xact_tracking")
				statsExec(t, p, b, c, "INSERT INTO xact_tracking VALUES(1),(2)", 2)
				oid := xactBlockOID(t, p, c, "xact_tracking")
				peer := statActivityConn(t, p, b)
				statsExec(t, p, b, c, "SET stats_fetch_consistency="+mode, 0)
				xactBlockBegin(t, p, b, c)
				defer c.ExecContext(context.Background(), "ROLLBACK")
				getter, err := c.PrepareContext(p.Ctx, xactBlockSQL)
				if err != nil {
					t.Fatal(err)
				}
				defer getter.Close()
				base := xactBlockRead(t, p, c, getter, oid)
				statsNoTimestamp(t, p, c, nil)
				xactBlockRows(t, p, b, c, "SELECT id FROM xact_tracking ORDER BY id", 1, 2)
				first := xactBlockRead(t, p, c, getter, oid)
				xactBlockAdvanced(t, b, base, first)
				statsNoTimestamp(t, p, c, nil)
				statsClear(t, p, b, c)
				xactBlockWant(t, xactBlockRead(t, p, c, getter, oid), first)
				statsNoTimestamp(t, p, c, nil)
				xactBlockRows(t, p, b, c, "SELECT id FROM xact_tracking ORDER BY id", 1, 2)
				second := xactBlockRead(t, p, c, getter, oid)
				xactBlockAdvanced(t, b, first, second)
				// Deliberately construct a genuine cumulative snapshot separately.
				statsExec(t, p, b, c, "SET stats_fetch_consistency=snapshot", 0)
				statsClear(t, p, b, c)
				statsCount(t, p, c, "xact_tracking")
				stamp := statsTimestamp(t, p, c, nil)
				if !stamp.Valid {
					t.Fatal("cumulative read failed to capture a snapshot")
				}
				xactBlockWant(t, xactBlockRead(t, p, c, getter, oid), second)
				xactBlockRows(t, p, b, c, "SELECT id FROM xact_tracking ORDER BY id", 1, 2)
				third := xactBlockRead(t, p, c, getter, oid)
				xactBlockAdvanced(t, b, second, third)
				if after := statsTimestamp(t, p, c, nil); !after.Valid || !after.Time.Equal(stamp.Time) {
					t.Fatal("pending getter replaced cumulative snapshot", stamp, after)
				}
				statsClear(t, p, b, c)
				xactBlockWant(t, xactBlockRead(t, p, c, getter, oid), third)
				statsNoTimestamp(t, p, c, nil)
				statsExec(t, p, b, c, "SET LOCAL track_counts=off", 0)
				xactBlockRows(t, p, b, c, "SELECT id FROM xact_tracking ORDER BY id", 1, 2)
				xactBlockWant(t, xactBlockRead(t, p, c, getter, oid), third)
				// Session-local suppression must not become a provider-global gate.
				statsExec(t, p, b, peer, "BEGIN", 0)
				defer peer.ExecContext(context.Background(), "ROLLBACK")
				peerBase := xactBlockRead(t, p, peer, nil, oid)
				xactBlockRows(t, p, b, peer, "SELECT id FROM xact_tracking ORDER BY id", 1, 2)
				xactBlockAdvanced(t, b, peerBase, xactBlockRead(t, p, peer, nil, oid))
				xactBlockWant(t, xactBlockRead(t, p, c, getter, oid), third)
				statsExec(t, p, b, c, "SAVEPOINT tracking_mark", 0)
				statsExec(t, p, b, c, "SET LOCAL track_counts=on", 0)
				for _, timing := range []string{"off", "on"} {
					statsExec(t, p, b, c, "SET LOCAL track_io_timing="+timing, 0)
					before := xactBlockRead(t, p, c, getter, oid)
					xactBlockRows(t, p, b, c, "SELECT id FROM xact_tracking ORDER BY id", 1, 2)
					xactBlockAdvanced(t, b, before, xactBlockRead(t, p, c, getter, oid))
				}
				beforeRollback := xactBlockRead(t, p, c, getter, oid)
				statsExec(t, p, b, c, "ROLLBACK TO tracking_mark", 0)
				xactBlockWant(t, xactBlockRead(t, p, c, getter, oid), beforeRollback)
				xactBlockRows(t, p, b, c, "SELECT id FROM xact_tracking ORDER BY id", 1, 2)
				xactBlockWant(t, xactBlockRead(t, p, c, getter, oid), beforeRollback)
				fixture.RunStep(t, p.Ctx, c, b.Recorder, fixture.Select("SELECT current_setting('track_counts') AS tracking", []string{"tracking"}, []string{"TEXT"}, [][]any{{"off"}}))
				statsExec(t, p, b, c, "ROLLBACK", 0)
				fixture.RunStep(t, p.Ctx, c, b.Recorder, fixture.Select("SELECT current_setting('track_counts') AS tracking", []string{"tracking"}, []string{"TEXT"}, [][]any{{"on"}}))
				// Transactional SET rolls back as well; it is not a global default.
				statsExec(t, p, b, c, "BEGIN", 0)
				statsExec(t, p, b, c, "SET track_counts=off", 0)
				statsExec(t, p, b, c, "ROLLBACK", 0)
				fixture.RunStep(t, p.Ctx, c, b.Recorder, fixture.Select("SELECT current_setting('track_counts') AS tracking", []string{"tracking"}, []string{"TEXT"}, [][]any{{"on"}}))
				statsExec(t, p, b, c, "BEGIN", 0)
				statsExec(t, p, b, c, "SET LOCAL track_counts=off", 0)
				statsExec(t, p, b, c, "COMMIT", 0)
				fixture.RunStep(t, p.Ctx, c, b.Recorder, fixture.Select("SELECT current_setting('track_counts') AS tracking", []string{"tracking"}, []string{"TEXT"}, [][]any{{"on"}}))
				statsExec(t, p, b, c, "BEGIN", 0)
				for _, activity := range []string{"off", "on"} {
					statsExec(t, p, b, c, "SET LOCAL track_activities="+activity, 0)
					before := xactBlockRead(t, p, c, getter, oid)
					xactBlockRows(t, p, b, c, "SELECT id FROM xact_tracking ORDER BY id", 1, 2)
					xactBlockAdvanced(t, b, before, xactBlockRead(t, p, c, getter, oid))
				}
				statsExec(t, p, b, c, "ROLLBACK", 0)
			})
		})
	}
}
