package reference_test

import (
	"database/sql"
	"testing"

	"stuntgres/reference/fixture"
)

func TestReferenceStatDatabaseSnapshotFirstAccess(t *testing.T) {
	for _, first := range []struct {
		name, query string
	}{
		{"index", "SELECT idx_scan FROM pg_catalog.pg_stat_all_indexes WHERE indexrelname='stat_db_snapshot_idx'"},
		{"archiver", "SELECT archived_count FROM pg_catalog.pg_stat_archiver"},
		{"bgwriter", "SELECT buffers_clean FROM pg_catalog.pg_stat_bgwriter"},
		{"slru", "SELECT blks_hit FROM pg_catalog.pg_stat_slru WHERE name='transaction'"},
	} {
		t.Run(first.name, func(t *testing.T) {
			// Each first-access authority owns a fresh fixture/context, not a
			// shared budget consumed by earlier publication/lifecycle vectors.
			statDatabaseScenario(t, "first_"+first.name, func(t *testing.T, p *fixture.Pair, b fixture.Backend, observer *sql.Conn, create func(string) fixture.Backend) {
				statsExec(t, p, b, observer, "CREATE TABLE stat_db_snapshot_local(id integer)", 0)
				statsExec(t, p, b, observer, "CREATE INDEX stat_db_snapshot_idx ON stat_db_snapshot_local USING hash(id)", 0)
				left, right := create("b"), create("c")
				baseLeft := statDatabaseSetup(t, p, left, observer)
				baseRight := statDatabaseSetup(t, p, right, observer)
				fresh, err := b.DB.Conn(p.Ctx)
				if err != nil {
					t.Fatal(err)
				}
				defer fresh.Close()
				statsExec(t, p, b, fresh, "SET stats_fetch_consistency=none", 0)
				publish := func(target fixture.Backend, before statDatabaseVector, query string, inserted int64) statDatabaseVector {
					t.Helper()
					writer, retire := statDatabaseConn(t, p, target)
					statsExec(t, p, target, writer, "BEGIN", 0)
					statsExec(t, p, target, writer, query, inserted)
					statsExec(t, p, target, writer, "COMMIT", 0)
					retire()
					published := statDatabaseAwait(t, p, fresh, target.Name, func(v statDatabaseVector) bool {
						return v.Backends == 0 && v.Insert == before.Insert+inserted && v.Sessions == before.Sessions+1
					})
					statDatabaseWantRetiredDelta(t, before, published, statDatabaseStartupCommits(target)+1, 0, inserted, 0, 0, 1, 0, 0, 0)
					return published
				}
				seedLeft := publish(left, baseLeft, "INSERT INTO stat_db_rows VALUES(1),(2)", 2)
				seedRight := publish(right, baseRight, "INSERT INTO stat_db_rows VALUES(1),(2),(3)", 3)
				if seedLeft.Insert <= 0 || seedRight.Insert <= 0 || seedLeft.Insert == seedRight.Insert {
					t.Fatal("first-access witnesses require distinct positive published B/C inserts", seedLeft, seedRight)
				}
				firstAccess := func() {
					t.Helper()
					var count int64
					// Project a cumulative field from an actual row. Identity-only,
					// empty and count(*) queries do not prove this read authority.
					if err := observer.QueryRowContext(p.Ctx, first.query).Scan(&count); err != nil {
						t.Fatal(err)
					}
					if count < 0 || first.name == "index" && count != 0 {
						t.Fatal("invalid first cumulative row", first.name, count)
					}
				}
				wantPeers := func(phase string, wantLeft, wantRight statDatabaseVector) {
					t.Helper()
					gotLeft := statDatabaseGet(t, p, observer, left.Name)
					gotRight := statDatabaseGet(t, p, observer, right.Name)
					if gotLeft != wantLeft || gotRight != wantRight {
						t.Fatalf("%s: B got %+v want %+v; C got %+v want %+v", phase, gotLeft, wantLeft, gotRight, wantRight)
					}
				}
				statsExec(t, p, b, observer, "SET stats_fetch_consistency=snapshot", 0)
				statsExec(t, p, b, observer, "BEGIN", 0)
				defer observer.ExecContext(p.Ctx, "ROLLBACK")
				statsNoTimestamp(t, p, observer, nil)
				firstAccess()
				stamp := statsTimestamp(t, p, observer, nil)
				if !stamp.Valid {
					t.Fatal("first cumulative row did not construct the snapshot")
				}
				// Publish both peers before the first B/C read in the retained
				// transaction. A lazy per-database fix would incorrectly see these.
				newLeft := publish(left, seedLeft, "INSERT INTO stat_db_rows VALUES(11),(12),(13),(14)", 4)
				newRight := publish(right, seedRight, "INSERT INTO stat_db_rows VALUES(11),(12),(13),(14),(15)", 5)
				wantPeers("first-access retained positive cut", seedLeft, seedRight)
				if held := statsTimestamp(t, p, observer, nil); !held.Valid || !held.Time.Equal(stamp.Time) {
					t.Fatal("peer publication changed the retained timestamp", held, stamp)
				}
				// The none reader already proved publication. Clear only discards
				// A's read cut; it does not publish or flush a peer writer.
				statsClear(t, p, b, observer)
				statsNoTimestamp(t, p, observer, nil)
				firstAccess()
				wantPeers("clear exposes independently published peers", newLeft, newRight)
				cleared := statsTimestamp(t, p, observer, nil)
				if !cleared.Valid || cleared.Time.Before(stamp.Time) {
					t.Fatal("clear failed to establish a new capture", cleared, stamp)
				}
				endLeft := publish(left, newLeft, "INSERT INTO stat_db_rows VALUES(21),(22),(23),(24),(25),(26)", 6)
				endRight := publish(right, newRight, "INSERT INTO stat_db_rows VALUES(21),(22),(23),(24),(25),(26),(27)", 7)
				wantPeers("recaptured cut retained until transaction end", newLeft, newRight)
				if held := statsTimestamp(t, p, observer, nil); !held.Valid || !held.Time.Equal(cleared.Time) {
					t.Fatal("later peer publication changed the rebuilt timestamp", held, cleared)
				}
				statsExec(t, p, b, observer, "COMMIT", 0)
				statsExec(t, p, b, observer, "BEGIN", 0)
				statsNoTimestamp(t, p, observer, nil)
				firstAccess()
				wantPeers("transaction end exposes latest published peers", endLeft, endRight)
				statsExec(t, p, b, observer, "COMMIT", 0)
			})
		})
	}
}
