package reference_test

import (
	"bytes"
	"database/sql"
	"fmt"
	"stuntgres"
	"stuntgres/reference/fixture"
	"testing"
)

func TestReferenceQueryLockViewTargetOccurrences(t *testing.T) {
	queryLockViewTargetOccurrences(t)
}

// Expected rows and physical ownership are independent of deparser spelling.
// Dump/Restore and PostgreSQL replay consume the actual emitted native SQL.
func queryLockViewTargetOccurrences(t *testing.T) {
	for _, prepared := range []bool{false, true} {
		for _, tc := range []struct {
			name, from, clauses, primary, other string
			second, shadow                      bool
		}{
			{"mixed-forward", "t CROSS JOIN t b", "FOR UPDATE OF t NOWAIT", "t", "t", false, false},
			{"mixed-reverse", "t b CROSS JOIN t", "FOR UPDATE OF t NOWAIT", "t", "t", false, false},
			{"repeated-forward", "t CROSS JOIN t b", "FOR KEY SHARE OF t SKIP LOCKED FOR UPDATE OF t NOWAIT", "t", "t", false, false},
			{"repeated-reverse", "t b CROSS JOIN t", "FOR UPDATE OF t SKIP LOCKED FOR SHARE OF t NOWAIT", "t", "t", false, false},
			{"both-contributors", "t CROSS JOIN t b", "FOR UPDATE OF t NOWAIT FOR SHARE OF b SKIP LOCKED", "t", "t", true, false},
			{"alias-matches-other-base", "u t CROSS JOIN t b", "FOR UPDATE OF t NOWAIT", "u", "t", false, false},
			{"qualified-forward", "public.t t CROSS JOIN lock_shadow.t b", "FOR UPDATE OF t NOWAIT", "t", "lock_shadow.t", false, true},
			{"qualified-reverse", "lock_shadow.t b CROSS JOIN public.t t", "FOR UPDATE OF t NOWAIT", "t", "lock_shadow.t", false, true},
		} {
			t.Run(fmt.Sprintf("%s/prepared=%v", tc.name, prepared), func(t *testing.T) {
				fixture.QueryLockScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, a, peer *sql.Conn) {
					if tc.shadow {
						fixture.LockExec(t, p.Ctx, a, "CREATE SCHEMA lock_shadow", 0)
						t.Cleanup(func() {
							if _, err := a.ExecContext(p.Ctx, "DROP SCHEMA lock_shadow CASCADE"); err != nil {
								t.Error(err)
							}
						})
						fixture.LockExec(t, p.Ctx, a, "CREATE TABLE lock_shadow.t(id INT,v INT)", 0)
						fixture.LockExec(t, p.Ctx, a, "INSERT INTO lock_shadow.t VALUES(1,10),(2,20),(3,30)", 3)
					}
					fixture.LockExec(t, p.Ctx, a, "CREATE VIEW selected_self AS SELECT t.id FROM "+tc.from+" WHERE t.id=1 AND b.id=2 "+tc.clauses, 0)
					queryLockOccurrenceOwnership(t, p, b, a, peer, tc.primary, tc.other, prepared, tc.second)
					fixture.LockExec(t, p.Ctx, a, "ALTER TABLE "+tc.primary+" RENAME TO renamed", 0)
					other := tc.other
					if other == tc.primary {
						other = "renamed"
					}
					queryLockOccurrenceOwnership(t, p, b, a, peer, "renamed", other, prepared, tc.second)
					if !b.PG {
						var dump bytes.Buffer
						if err := stuntgres.Dump(p.Ctx, p.Native, &dump); err != nil {
							t.Fatal("occurrence-owned OF dump", err)
						}
						if err := stuntgres.Restore(p.Ctx, p.Native, bytes.NewReader(dump.Bytes())); err != nil {
							t.Fatal("occurrence-owned OF restore", err)
						}
						queryLockOccurrenceOwnership(t, p, b, a, peer, "renamed", other, prepared, tc.second)
						if p.Postgres != nil {
							// Register before replay so partial execution/failure also retires objects.
							t.Cleanup(func() {
								if _, err := p.Postgres.ExecContext(p.Ctx, "DROP VIEW IF EXISTS selected_self; DROP TABLE IF EXISTS t,u,renamed; DROP SCHEMA IF EXISTS lock_shadow CASCADE"); err != nil {
									t.Error(err)
								}
							})
							if _, err := p.Postgres.ExecContext(p.Ctx, dump.String()); err != nil {
								t.Fatal("actual emitted occurrence OF SQL replay", err)
							}
							replayA, replayPeer := fixture.LockConnections(t, p.Ctx, p.Postgres)
							replay := fixture.Backend{Name: "emitted-postgres", DB: p.Postgres, Recorder: p.PostgresRecord, PG: true}
							queryLockOccurrenceOwnership(t, p, replay, replayA, replayPeer, "renamed", other, prepared, tc.second)
						}
					}
				})
			})
		}
	}
}

func queryLockOccurrenceOwnership(t *testing.T, p *fixture.Pair, b fixture.Backend, a, peer *sql.Conn, primary, other string, prepared, second bool) {
	t.Helper()
	holder := fixture.QueryLockBegin(t, p.Ctx, a)
	fixture.QueryLockRows(t, p, b, holder, "SELECT id FROM selected_self ORDER BY id", prepared, "id", 1)
	// Rows and any prepared statement are closed by queryLockRows; both selected
	// contributors still belong to the transaction, not to the cursor or spelling.
	queryLockMigratedBlocked(t, p.Ctx, peer, b.PG, "UPDATE "+primary+" SET v=v WHERE id=1", prepared)
	fixture.QueryLockNowait(t, p, b, peer, "SELECT id FROM "+primary+" WHERE id=1 FOR KEY SHARE NOWAIT", prepared, primary)
	if second {
		queryLockMigratedBlocked(t, p.Ctx, peer, b.PG, "UPDATE "+other+" SET v=v WHERE id=2", prepared)
		fixture.QueryLockRows(t, p, b, peer, "SELECT id FROM "+other+" WHERE id=2 FOR KEY SHARE NOWAIT", prepared, "id", 2)
	} else {
		fixture.RowAffected(t, p.Ctx, peer, "UPDATE "+other+" SET v=v WHERE id=2", prepared, 1)
	}
	fixture.RowAffected(t, p.Ctx, peer, "UPDATE "+primary+" SET v=v WHERE id=3", prepared, 1)
	if err := holder.Rollback(); err != nil {
		t.Fatal(err)
	}
	fixture.RowAffected(t, p.Ctx, peer, "UPDATE "+primary+" SET v=v WHERE id=1", prepared, 1)
	fixture.RowAffected(t, p.Ctx, peer, "UPDATE "+other+" SET v=v WHERE id=2", prepared, 1)
	blocker := fixture.QueryLockBegin(t, p.Ctx, peer)
	fixture.QueryLockRows(t, p, b, blocker, "SELECT id FROM "+primary+" WHERE id=1 FOR NO KEY UPDATE", false, "id", 1)
	fixture.QueryLockNowait(t, p, b, a, "SELECT id FROM selected_self ORDER BY id", prepared, primary)
	if err := blocker.Rollback(); err != nil {
		t.Fatal(err)
	}
	fixture.QueryLockRows(t, p, b, a, "SELECT id FROM selected_self ORDER BY id", prepared, "id", 1)
	fixture.RowAffected(t, p.Ctx, peer, "UPDATE "+primary+" SET v=v WHERE id=1", prepared, 1)
}
