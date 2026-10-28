package reference_test

import (
	"database/sql"
	"testing"

	"stuntgres/reference/fixture"
)

func legacySnapshotReadPair(t *testing.T, p *fixture.Pair, c *sql.Conn, held *sql.Stmt) string {
	t.Helper()
	var legacy, modern string
	var err error
	if held != nil {
		err = held.QueryRowContext(p.Ctx).Scan(&legacy, &modern)
	} else {
		err = c.QueryRowContext(p.Ctx, "SELECT txid_current_snapshot()::text,pg_current_snapshot()::text").Scan(&legacy, &modern)
	}
	if err != nil || legacy != modern {
		t.Fatalf("one command snapshot %s/%s: %v", legacy, modern, err)
	}
	snapshotParse(t, legacy)
	return legacy
}

func TestReferenceLegacySnapshotIsolation(t *testing.T) {
	for _, isolation := range []string{"READ COMMITTED", "REPEATABLE READ", "SERIALIZABLE"} {
		for _, prepared := range []bool{false, true} {
			name := isolation
			if prepared {
				name += "-held"
			}
			t.Run(name, func(t *testing.T) {
				legacySnapshotScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend) {
					a, bumper, o := snapshotConn(t, p, b), snapshotConn(t, p, b), snapshotConn(t, p, b)
					var held *sql.Stmt
					if prepared {
						var err error
						held, err = o.PrepareContext(p.Ctx, "SELECT txid_current_snapshot()::text,pg_current_snapshot()::text")
						if err != nil {
							t.Fatal(err)
						}
						defer held.Close()
					}
					fixture.LockExec(t, p.Ctx, a, "BEGIN", 0)
					id := identityID(t, p, a)
					snapshotBump(t, p, bumper, id)
					fixture.LockExec(t, p.Ctx, o, "BEGIN ISOLATION LEVEL "+isolation, 0)
					identityBoth(t, p, b, o, legacySnapshotStep(b, "SELECT txid_current_if_assigned() AS id", []string{"id"}, []string{"INT8"}, [][]any{{nil}}))
					s := legacySnapshotReadPair(t, p, o, held)
					snapshotPeerPresent(t, s, id, true)
					identityBoth(t, p, b, o, legacySnapshotStep(b, "SELECT txid_snapshot_xmin($1) AS lo,txid_snapshot_xmax($1) AS hi", []string{"lo", "hi"}, []string{"INT8", "INT8"}, [][]any{{int64(snapshotParse(t, s).min), int64(snapshotParse(t, s).max)}}, s))
					fixture.LockExec(t, p.Ctx, o, "SAVEPOINT retained", 0)
					fixture.LockExec(t, p.Ctx, a, "COMMIT", 0)
					fixture.LockExec(t, p.Ctx, o, "ROLLBACK TO retained", 0)
					after := legacySnapshotReadPair(t, p, o, held)
					if isolation != "READ COMMITTED" && after != s {
						t.Fatalf("stable snapshot changed %s -> %s", s, after)
					}
					snapshotPeerPresent(t, after, id, isolation != "READ COMMITTED")
					identityBoth(t, p, b, o, fixture.Select("SELECT txid_visible_in_snapshot($1,$2) AS v", []string{"v"}, []string{"BOOL"}, [][]any{{isolation == "READ COMMITTED"}}, int64(snapshotNumber(t, id)), after))
					identityBoth(t, p, b, o, fixture.Select("SELECT txid_visible_in_snapshot($1,$2) AS v", []string{"v"}, []string{"BOOL"}, [][]any{{false}}, int64(snapshotNumber(t, id)), s))
					identityBoth(t, p, b, o, legacySnapshotStep(b, "SELECT txid_current_if_assigned() AS id", []string{"id"}, []string{"INT8"}, [][]any{{nil}}))
					fixture.LockExec(t, p.Ctx, o, "COMMIT", 0)
					fresh := legacySnapshotReadPair(t, p, o, held)
					snapshotPeerPresent(t, fresh, id, false)
					identityBoth(t, p, b, o, fixture.Select("SELECT txid_visible_in_snapshot($1,$2) AS v", []string{"v"}, []string{"BOOL"}, [][]any{{true}}, int64(snapshotNumber(t, id)), fresh))
				})
			})
		}
	}
	t.Run("self-excluded", func(t *testing.T) {
		legacySnapshotScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend) {
			a, bumper := snapshotConn(t, p, b), snapshotConn(t, p, b)
			fixture.LockExec(t, p.Ctx, a, "BEGIN", 0)
			identityUnassigned(t, p, a)
			legacySnapshotReadPair(t, p, a, nil)
			identityUnassigned(t, p, a)
			id := identityID(t, p, a)
			snapshotBump(t, p, bumper, id)
			s := legacySnapshotReadPair(t, p, a, nil)
			snapshotPeerPresent(t, s, id, false)
			identityBoth(t, p, b, a, fixture.Select("SELECT txid_visible_in_snapshot($1,$2) AS v", []string{"v"}, []string{"BOOL"}, [][]any{{true}}, int64(snapshotNumber(t, id)), s))
			fixture.LockExec(t, p.Ctx, a, "ROLLBACK", 0)
		})
	})
}
