package reference_test

import (
	"database/sql"
	"fmt"
	"testing"
)

func TestReferenceQueryLockDistinctFrontier(t *testing.T) {
	queryLockDistinctFrontier(t)
}

// All expected tuples, metadata and physical contributor holds below are
// independently specified. The root pins new SQL before native acceptance.
func queryLockDistinctFrontier(t *testing.T) {
	for _, prepared := range []bool{false, true} {
		for _, policy := range []string{"", "SKIP LOCKED", "NOWAIT"} {
			for _, zero := range []bool{false, true} {
				t.Run(fmt.Sprintf("contributors/%s/limit0=%v/prepared=%v", policy, zero, prepared), func(t *testing.T) {
					queryLockScenario(t, func(t *testing.T, p *referencePair, b referenceBackend, a, peer *sql.Conn) {
						queryLockDistinctInput(t, p, a, policy)
						observer, err := b.db.Conn(p.ctx)
						if err != nil {
							t.Fatal(err)
						}
						t.Cleanup(func() { observer.Close() })
						var holder *sql.Tx
						if zero {
							holder = queryLockBegin(t, p.ctx, a)
							queryLockRows(t, p, b, holder, "SELECT id FROM t WHERE id=1 FOR UPDATE", false, "id", 1)
						}
						reader := queryLockBegin(t, p.ctx, peer)
						q := "SELECT DISTINCT v FROM marked ORDER BY v"
						if zero {
							q += " LIMIT 0"
							queryLockRows(t, p, b, reader, q, prepared, "v")
						} else {
							queryLockRows(t, p, b, reader, q, prepared, "v", 10)
							// Exhaustion and Rows/Stmt closure must not discard either duplicate's hold.
							queryLockMigratedBlocked(t, p.ctx, observer, b.pg, "UPDATE t SET v=v WHERE id=1", prepared)
							queryLockMigratedBlocked(t, p.ctx, observer, b.pg, "UPDATE t SET v=v WHERE id=2", prepared)
							queryLockRows(t, p, b, reader, "SELECT id FROM t WHERE id=1 FOR UPDATE NOWAIT", true, "id", 1)
						}
						referenceRowAffected(t, p.ctx, observer, "UPDATE t SET v=v WHERE id=3", prepared, 1)
						if zero {
							referenceRowAffected(t, p.ctx, observer, "UPDATE t SET v=v WHERE id=2", prepared, 1)
							queryLockNowait(t, p, b, observer, "SELECT id FROM t WHERE id=1 FOR UPDATE NOWAIT", false, "t")
							if err := holder.Rollback(); err != nil {
								t.Fatal(err)
							}
							// Reader is still active: LIMIT0 must not have retained a phantom grant.
							referenceRowAffected(t, p.ctx, observer, "UPDATE t SET v=v WHERE id=1", prepared, 1)
						}
						if err := reader.Rollback(); err != nil {
							t.Fatal(err)
						}
						referenceRowAffected(t, p.ctx, peer, "UPDATE t SET v=v", prepared, 3)
					})
				})
			}
		}
		t.Run(fmt.Sprintf("skip-duplicate/prepared=%v", prepared), func(t *testing.T) {
			queryLockScenario(t, func(t *testing.T, p *referencePair, b referenceBackend, a, peer *sql.Conn) {
				queryLockDistinctInput(t, p, a, "SKIP LOCKED")
				observer, err := b.db.Conn(p.ctx)
				if err != nil {
					t.Fatal(err)
				}
				t.Cleanup(func() { observer.Close() })
				holder := queryLockBegin(t, p.ctx, a)
				queryLockRows(t, p, b, holder, "SELECT id FROM t WHERE id=1 FOR UPDATE", false, "id", 1)
				reader := queryLockBegin(t, p.ctx, peer)
				q := "SELECT DISTINCT v FROM marked ORDER BY v"
				queryLockRows(t, p, b, reader, q, prepared, "v", 10)
				queryLockMigratedBlocked(t, p.ctx, observer, b.pg, "UPDATE t SET v=v WHERE id=2", prepared)
				referenceRowAffected(t, p.ctx, observer, "UPDATE t SET v=v WHERE id=3", prepared, 1)
				queryLockRows(t, p, b, reader, q, prepared, "v", 10)
				if err := holder.Rollback(); err != nil {
					t.Fatal(err)
				}
				// An unavailable duplicate did not leave any reader ownership behind.
				referenceRowAffected(t, p.ctx, observer, "UPDATE t SET v=v WHERE id=1", prepared, 1)
				queryLockMigratedBlocked(t, p.ctx, observer, b.pg, "UPDATE t SET v=v WHERE id=2", prepared)
				if err := reader.Rollback(); err != nil {
					t.Fatal(err)
				}
				referenceRowAffected(t, p.ctx, peer, "UPDATE t SET v=v", prepared, 3)
			})
		})
		for _, explicit := range []bool{false, true} {
			t.Run(fmt.Sprintf("nowait-prefix/explicit=%v/prepared=%v", explicit, prepared), func(t *testing.T) {
				queryLockScenario(t, func(t *testing.T, p *referencePair, b referenceBackend, a, peer *sql.Conn) {
					queryLockDistinctInput(t, p, a, "NOWAIT")
					holder := queryLockBegin(t, p.ctx, a)
					queryLockRows(t, p, b, holder, "SELECT id FROM t WHERE id=2 FOR UPDATE", false, "id", 2)
					var x referenceExecutor = peer
					var reader *sql.Tx
					if explicit {
						reader = queryLockBegin(t, p.ctx, peer)
						x = reader
					}
					q := "SELECT DISTINCT v FROM marked ORDER BY v"
					out := referenceRowQuery(p.ctx, x, q, prepared, b.recorder.rawBackend)
					// The child's ORDER guarantees internal row1 acquisition before row2 fails.
					// DISTINCT drains/materializes before publishing: no successful public v10
					// prefix is expected, even if pq exposes an empty cursor with terminal error.
					terminal := queryLockErrorOutcome(t, out, b.pg, "v", nil)
					want := controlsReferenceDiagnostic("55P03", `could not obtain lock on row in relation "t"`)
					referenceAssertDiagnostic(t, terminal, &want)
					if explicit {
						referenceRunStep(t, p.ctx, reader, b.recorder, referenceImplicitError("SELECT id FROM t", referenceTransactionAborted()))
						if err := reader.Rollback(); err != nil {
							t.Fatal(err)
						}
					}
					// Error cursors/statements have already closed before recovery SQL.
					referenceRowAffected(t, p.ctx, peer, "UPDATE t SET v=v WHERE id=1", prepared, 1)
					referenceRowAffected(t, p.ctx, peer, "UPDATE t SET v=v WHERE id=3", prepared, 1)
					queryLockRows(t, p, b, peer, "SELECT DISTINCT v FROM t WHERE id=1 ORDER BY v", prepared, "v", 10)
					queryLockNowait(t, p, b, peer, "SELECT id FROM t WHERE id=2 FOR UPDATE NOWAIT", false, "t")
					queryLockRows(t, p, b, holder, "SELECT id FROM t WHERE id=2 FOR UPDATE NOWAIT", true, "id", 2)
					if err := holder.Rollback(); err != nil {
						t.Fatal(err)
					}
					queryLockRows(t, p, b, peer, q, prepared, "v", 10)
					referenceRowAffected(t, p.ctx, peer, "UPDATE t SET v=v", prepared, 3)
				})
			})
		}
	}
}

func queryLockDistinctInput(t *testing.T, p *referencePair, a *sql.Conn, policy string) {
	t.Helper()
	referenceRowAffected(t, p.ctx, a, "DELETE FROM t", false, 4)
	referenceRowAffected(t, p.ctx, a, "INSERT INTO t VALUES(1,10),(2,10),(3,20)", false, 3)
	referenceLockExec(t, p.ctx, a, "CREATE VIEW marked AS SELECT id,v FROM t WHERE id<3 ORDER BY id FOR UPDATE "+policy, 0)
}
