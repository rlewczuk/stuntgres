package reference_test

import (
	"bytes"
	"database/sql"
	"fmt"
	"testing"

	"stuntgres"
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
				queryLockScenario(t, func(t *testing.T, p *referencePair, b referenceBackend, a, peer *sql.Conn) {
					if tc.shadow {
						referenceLockExec(t, p.ctx, a, "CREATE SCHEMA lock_shadow", 0)
						t.Cleanup(func() {
							if _, err := a.ExecContext(p.ctx, "DROP SCHEMA lock_shadow CASCADE"); err != nil {
								t.Error(err)
							}
						})
						referenceLockExec(t, p.ctx, a, "CREATE TABLE lock_shadow.t(id INT,v INT)", 0)
						referenceLockExec(t, p.ctx, a, "INSERT INTO lock_shadow.t VALUES(1,10),(2,20),(3,30)", 3)
					}
					referenceLockExec(t, p.ctx, a, "CREATE VIEW selected_self AS SELECT t.id FROM "+tc.from+" WHERE t.id=1 AND b.id=2 "+tc.clauses, 0)
					queryLockOccurrenceOwnership(t, p, b, a, peer, tc.primary, tc.other, prepared, tc.second)
					referenceLockExec(t, p.ctx, a, "ALTER TABLE "+tc.primary+" RENAME TO renamed", 0)
					other := tc.other
					if other == tc.primary {
						other = "renamed"
					}
					queryLockOccurrenceOwnership(t, p, b, a, peer, "renamed", other, prepared, tc.second)
					if !b.pg {
						var dump bytes.Buffer
						if err := stuntgres.Dump(p.ctx, p.native, &dump); err != nil {
							t.Fatal("occurrence-owned OF dump", err)
						}
						if err := stuntgres.Restore(p.ctx, p.native, bytes.NewReader(dump.Bytes())); err != nil {
							t.Fatal("occurrence-owned OF restore", err)
						}
						queryLockOccurrenceOwnership(t, p, b, a, peer, "renamed", other, prepared, tc.second)
						if p.postgres != nil {
							// Register before replay so partial execution/failure also retires objects.
							t.Cleanup(func() {
								if _, err := p.postgres.ExecContext(p.ctx, "DROP VIEW IF EXISTS selected_self; DROP TABLE IF EXISTS t,u,renamed; DROP SCHEMA IF EXISTS lock_shadow CASCADE"); err != nil {
									t.Error(err)
								}
							})
							if _, err := p.postgres.ExecContext(p.ctx, dump.String()); err != nil {
								t.Fatal("actual emitted occurrence OF SQL replay", err)
							}
							replayA, replayPeer := referenceLockConnections(t, p.ctx, p.postgres)
							replay := referenceBackend{name: "emitted-postgres", db: p.postgres, recorder: p.postgresRecord, pg: true}
							queryLockOccurrenceOwnership(t, p, replay, replayA, replayPeer, "renamed", other, prepared, tc.second)
						}
					}
				})
			})
		}
	}
}

func queryLockOccurrenceOwnership(t *testing.T, p *referencePair, b referenceBackend, a, peer *sql.Conn, primary, other string, prepared, second bool) {
	t.Helper()
	holder := queryLockBegin(t, p.ctx, a)
	queryLockRows(t, p, b, holder, "SELECT id FROM selected_self ORDER BY id", prepared, "id", 1)
	// Rows and any prepared statement are closed by queryLockRows; both selected
	// contributors still belong to the transaction, not to the cursor or spelling.
	queryLockMigratedBlocked(t, p.ctx, peer, b.pg, "UPDATE "+primary+" SET v=v WHERE id=1", prepared)
	queryLockNowait(t, p, b, peer, "SELECT id FROM "+primary+" WHERE id=1 FOR KEY SHARE NOWAIT", prepared, primary)
	if second {
		queryLockMigratedBlocked(t, p.ctx, peer, b.pg, "UPDATE "+other+" SET v=v WHERE id=2", prepared)
		queryLockRows(t, p, b, peer, "SELECT id FROM "+other+" WHERE id=2 FOR KEY SHARE NOWAIT", prepared, "id", 2)
	} else {
		referenceRowAffected(t, p.ctx, peer, "UPDATE "+other+" SET v=v WHERE id=2", prepared, 1)
	}
	referenceRowAffected(t, p.ctx, peer, "UPDATE "+primary+" SET v=v WHERE id=3", prepared, 1)
	if err := holder.Rollback(); err != nil {
		t.Fatal(err)
	}
	referenceRowAffected(t, p.ctx, peer, "UPDATE "+primary+" SET v=v WHERE id=1", prepared, 1)
	referenceRowAffected(t, p.ctx, peer, "UPDATE "+other+" SET v=v WHERE id=2", prepared, 1)
	blocker := queryLockBegin(t, p.ctx, peer)
	queryLockRows(t, p, b, blocker, "SELECT id FROM "+primary+" WHERE id=1 FOR NO KEY UPDATE", false, "id", 1)
	queryLockNowait(t, p, b, a, "SELECT id FROM selected_self ORDER BY id", prepared, primary)
	if err := blocker.Rollback(); err != nil {
		t.Fatal(err)
	}
	queryLockRows(t, p, b, a, "SELECT id FROM selected_self ORDER BY id", prepared, "id", 1)
	referenceRowAffected(t, p.ctx, peer, "UPDATE "+primary+" SET v=v WHERE id=1", prepared, 1)
}
