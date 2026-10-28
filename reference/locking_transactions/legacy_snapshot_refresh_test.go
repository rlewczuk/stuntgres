package reference_test

import (
	"testing"

	"stuntgres/reference/fixture"
)

func TestReferenceLegacySnapshotViewRefresh(t *testing.T) {
	legacySnapshotScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend) {
		a, bumper, o := snapshotConn(t, p, b), snapshotConn(t, p, b), snapshotConn(t, p, b)
		fixture.LockExec(t, p.Ctx, a, "BEGIN", 0)
		id := identityID(t, p, a)
		snapshotBump(t, p, bumper, id)
		fixture.LockExec(t, p.Ctx, o, "CREATE VIEW legacy_refresh AS SELECT txid_current_snapshot() AS s", 0)
		stmt, err := o.PrepareContext(p.Ctx, "SELECT s::text FROM legacy_refresh")
		if err != nil {
			t.Fatal(err)
		}
		defer stmt.Close()
		var before, after string
		if err := stmt.QueryRowContext(p.Ctx).Scan(&before); err != nil {
			t.Fatal(err)
		}
		snapshotPeerPresent(t, before, id, true)
		fixture.LockExec(t, p.Ctx, a, "COMMIT", 0)
		if err := stmt.QueryRowContext(p.Ctx).Scan(&after); err != nil {
			t.Fatal(err)
		}
		snapshotPeerPresent(t, after, id, false)
		for _, tc := range []struct {
			s       string
			visible bool
		}{{before, false}, {after, true}} {
			identityBoth(t, p, b, o, fixture.Select("SELECT txid_visible_in_snapshot($1,$2) AS v", []string{"v"}, []string{"BOOL"}, [][]any{{tc.visible}}, int64(snapshotNumber(t, id)), tc.s))
		}
	})
}

func TestReferenceLegacySnapshotCursors(t *testing.T) {
	legacySnapshotScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend) {
		var want [][]any
		for _, a := range []int64{13, 15, 18} {
			for _, z := range []int64{13, 15, 18} {
				want = append(want, []any{a, z})
			}
		}
		identityBoth(t, p, b, b.DB, fixture.Select("SELECT a.x,b.x FROM txid_snapshot_xip($1) AS a(x) CROSS JOIN txid_snapshot_xip($1) AS b(x) ORDER BY a.x,b.x", []string{"x", "x"}, []string{"INT8", "INT8"}, want, "12:20:13,15,18"))
		// Payload iteration is unsigned, but an explicit bigint ORDER BY is signed.
		identityBoth(t, p, b, b.DB, legacySnapshotStep(b, "SELECT x FROM txid_snapshot_xip($1) AS s(x) ORDER BY x", []string{"x"}, []string{"INT8"}, [][]any{{int64(-9223372036854775808)}, {int64(-9223372036854775806)}, {int64(9223372036854775807)}}, "9223372036854775807:9223372036854775811:9223372036854775807,9223372036854775808,9223372036854775810"))
	})
}

func TestReferenceLegacySnapshotCoercions(t *testing.T) {
	legacySnapshotScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend) {
		fixture.RunStep(t, p.Ctx, b.DB, b.Recorder, fixture.Exec("CREATE DOMAIN legacy_small AS smallint", 0))
		identityBoth(t, p, b, b.DB, fixture.Select("SELECT txid_visible_in_snapshot(11::legacy_small,'10:20:') AS v,txid_status(1::legacy_small) AS status", []string{"v", "status"}, []string{"BOOL", "TEXT"}, [][]any{{true, "committed"}}))
		identityBoth(t, p, b, b.DB, fixture.Select("SELECT txid_visible_in_snapshot(-1,'10:20:') AS v", []string{"v"}, []string{"BOOL"}, [][]any{{false}}))
	})
}
