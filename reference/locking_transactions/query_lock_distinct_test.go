package reference_test

import (
	"database/sql"
	"fmt"
	"stuntgres/reference/fixture"
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
					fixture.QueryLockScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, a, peer *sql.Conn) {
						queryLockDistinctInput(t, p, a, policy)
						observer, err := b.DB.Conn(p.Ctx)
						if err != nil {
							t.Fatal(err)
						}
						t.Cleanup(func() { observer.Close() })
						var holder *sql.Tx
						if zero {
							holder = fixture.QueryLockBegin(t, p.Ctx, a)
							fixture.QueryLockRows(t, p, b, holder, "SELECT id FROM t WHERE id=1 FOR UPDATE", false, "id", 1)
						}
						reader := fixture.QueryLockBegin(t, p.Ctx, peer)
						q := "SELECT DISTINCT v FROM marked ORDER BY v"
						if zero {
							q += " LIMIT 0"
							fixture.QueryLockRows(t, p, b, reader, q, prepared, "v")
						} else {
							fixture.QueryLockRows(t, p, b, reader, q, prepared, "v", 10)
							// Exhaustion and Rows/Stmt closure must not discard either duplicate's hold.
							queryLockMigratedBlocked(t, p.Ctx, observer, b.PG, "UPDATE t SET v=v WHERE id=1", prepared)
							queryLockMigratedBlocked(t, p.Ctx, observer, b.PG, "UPDATE t SET v=v WHERE id=2", prepared)
							fixture.QueryLockRows(t, p, b, reader, "SELECT id FROM t WHERE id=1 FOR UPDATE NOWAIT", true, "id", 1)
						}
						fixture.RowAffected(t, p.Ctx, observer, "UPDATE t SET v=v WHERE id=3", prepared, 1)
						if zero {
							fixture.RowAffected(t, p.Ctx, observer, "UPDATE t SET v=v WHERE id=2", prepared, 1)
							fixture.QueryLockNowait(t, p, b, observer, "SELECT id FROM t WHERE id=1 FOR UPDATE NOWAIT", false, "t")
							if err := holder.Rollback(); err != nil {
								t.Fatal(err)
							}
							// Reader is still active: LIMIT0 must not have retained a phantom grant.
							fixture.RowAffected(t, p.Ctx, observer, "UPDATE t SET v=v WHERE id=1", prepared, 1)
						}
						if err := reader.Rollback(); err != nil {
							t.Fatal(err)
						}
						fixture.RowAffected(t, p.Ctx, peer, "UPDATE t SET v=v", prepared, 3)
					})
				})
			}
		}
		t.Run(fmt.Sprintf("skip-duplicate/prepared=%v", prepared), func(t *testing.T) {
			fixture.QueryLockScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, a, peer *sql.Conn) {
				queryLockDistinctInput(t, p, a, "SKIP LOCKED")
				observer, err := b.DB.Conn(p.Ctx)
				if err != nil {
					t.Fatal(err)
				}
				t.Cleanup(func() { observer.Close() })
				holder := fixture.QueryLockBegin(t, p.Ctx, a)
				fixture.QueryLockRows(t, p, b, holder, "SELECT id FROM t WHERE id=1 FOR UPDATE", false, "id", 1)
				reader := fixture.QueryLockBegin(t, p.Ctx, peer)
				q := "SELECT DISTINCT v FROM marked ORDER BY v"
				fixture.QueryLockRows(t, p, b, reader, q, prepared, "v", 10)
				queryLockMigratedBlocked(t, p.Ctx, observer, b.PG, "UPDATE t SET v=v WHERE id=2", prepared)
				fixture.RowAffected(t, p.Ctx, observer, "UPDATE t SET v=v WHERE id=3", prepared, 1)
				fixture.QueryLockRows(t, p, b, reader, q, prepared, "v", 10)
				if err := holder.Rollback(); err != nil {
					t.Fatal(err)
				}
				// An unavailable duplicate did not leave any reader ownership behind.
				fixture.RowAffected(t, p.Ctx, observer, "UPDATE t SET v=v WHERE id=1", prepared, 1)
				queryLockMigratedBlocked(t, p.Ctx, observer, b.PG, "UPDATE t SET v=v WHERE id=2", prepared)
				if err := reader.Rollback(); err != nil {
					t.Fatal(err)
				}
				fixture.RowAffected(t, p.Ctx, peer, "UPDATE t SET v=v", prepared, 3)
			})
		})
		for _, explicit := range []bool{false, true} {
			t.Run(fmt.Sprintf("nowait-prefix/explicit=%v/prepared=%v", explicit, prepared), func(t *testing.T) {
				fixture.QueryLockScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, a, peer *sql.Conn) {
					queryLockDistinctInput(t, p, a, "NOWAIT")
					holder := fixture.QueryLockBegin(t, p.Ctx, a)
					fixture.QueryLockRows(t, p, b, holder, "SELECT id FROM t WHERE id=2 FOR UPDATE", false, "id", 2)
					var x fixture.Executor = peer
					var reader *sql.Tx
					if explicit {
						reader = fixture.QueryLockBegin(t, p.Ctx, peer)
						x = reader
					}
					q := "SELECT DISTINCT v FROM marked ORDER BY v"
					out := fixture.RowQuery(p.Ctx, x, q, prepared, b.Recorder.RawBackend)
					// The child's ORDER guarantees internal row1 acquisition before row2 fails.
					// DISTINCT drains/materializes before publishing: no successful public v10
					// prefix is expected, even if pq exposes an empty cursor with terminal error.
					terminal := fixture.QueryLockErrorOutcome(t, out, b.PG, "v", nil)
					want := fixture.ControlsReferenceDiagnostic("55P03", `could not obtain lock on row in relation "t"`)
					fixture.AssertDiagnostic(t, terminal, &want)
					if explicit {
						fixture.RunStep(t, p.Ctx, reader, b.Recorder, fixture.ImplicitError("SELECT id FROM t", fixture.TransactionAborted()))
						if err := reader.Rollback(); err != nil {
							t.Fatal(err)
						}
					}
					// Error cursors/statements have already closed before recovery SQL.
					fixture.RowAffected(t, p.Ctx, peer, "UPDATE t SET v=v WHERE id=1", prepared, 1)
					fixture.RowAffected(t, p.Ctx, peer, "UPDATE t SET v=v WHERE id=3", prepared, 1)
					fixture.QueryLockRows(t, p, b, peer, "SELECT DISTINCT v FROM t WHERE id=1 ORDER BY v", prepared, "v", 10)
					fixture.QueryLockNowait(t, p, b, peer, "SELECT id FROM t WHERE id=2 FOR UPDATE NOWAIT", false, "t")
					fixture.QueryLockRows(t, p, b, holder, "SELECT id FROM t WHERE id=2 FOR UPDATE NOWAIT", true, "id", 2)
					if err := holder.Rollback(); err != nil {
						t.Fatal(err)
					}
					fixture.QueryLockRows(t, p, b, peer, q, prepared, "v", 10)
					fixture.RowAffected(t, p.Ctx, peer, "UPDATE t SET v=v", prepared, 3)
				})
			})
		}
	}
}

func queryLockDistinctInput(t *testing.T, p *fixture.Pair, a *sql.Conn, policy string) {
	t.Helper()
	fixture.RowAffected(t, p.Ctx, a, "DELETE FROM t", false, 4)
	fixture.RowAffected(t, p.Ctx, a, "INSERT INTO t VALUES(1,10),(2,10),(3,20)", false, 3)
	fixture.LockExec(t, p.Ctx, a, "CREATE VIEW marked AS SELECT id,v FROM t WHERE id<3 ORDER BY id FOR UPDATE "+policy, 0)
}
