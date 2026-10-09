package reference_test

import (
	"context"
	"database/sql"
	"fmt"
	"testing"
	"time"
)

func TestReferenceQueryLockOptions(t *testing.T) {
	for _, prepared := range []bool{false, true} {
		for _, mode := range []string{"KEY SHARE", "SHARE", "NO KEY UPDATE", "UPDATE"} {
			t.Run(fmt.Sprintf("%s/%v", mode, prepared), func(t *testing.T) {
				queryLockScenario(t, func(t *testing.T, p *referencePair, b referenceBackend, a, peer *sql.Conn) {
					holder := queryLockBegin(t, p.ctx, a)
					queryLockRows(t, p, b, holder, "SELECT id FROM t WHERE id=1 FOR UPDATE", false, "id", 1)
					queryLockNowait(t, p, b, peer, "SELECT id FROM t alias WHERE id=1 FOR "+mode+" NOWAIT", prepared, "t")
					queryLockRows(t, p, b, peer, "SELECT id FROM t ORDER BY id FOR "+mode+" SKIP LOCKED", prepared, "id", 2, 3, 4)
					queryLockRows(t, p, b, holder, "SELECT id FROM t WHERE id=1 FOR "+mode+" SKIP LOCKED", prepared, "id", 1)
					queryLockRows(t, p, b, peer, "SELECT id FROM t ORDER BY id", prepared, "id", 1, 2, 3, 4)
					queryLockRows(t, p, b, holder, "SELECT id FROM t ORDER BY id FOR UPDATE", false, "id", 1, 2, 3, 4)
					queryLockRows(t, p, b, peer, "SELECT id FROM t ORDER BY id FOR "+mode+" SKIP LOCKED", prepared, "id")
					if err := holder.Rollback(); err != nil {
						t.Fatal(err)
					}
					referenceRowAffected(t, p.ctx, peer, "UPDATE t SET v=v", prepared, 4)
				})
			})
		}
	}
	// Complete strength compatibility matrix, using each conditional row policy.
	modes := []string{"KEY SHARE", "SHARE", "NO KEY UPDATE", "UPDATE"}
	for i, held := range modes {
		for j, wanted := range modes {
			for _, policy := range []string{"NOWAIT", "SKIP LOCKED"} {
				t.Run(held+"/"+wanted+"/"+policy, func(t *testing.T) {
					queryLockScenario(t, func(t *testing.T, p *referencePair, b referenceBackend, a, peer *sql.Conn) {
						holder := queryLockBegin(t, p.ctx, a)
						queryLockRows(t, p, b, holder, "SELECT id FROM t WHERE id=1 FOR "+held, false, "id", 1)
						compatible := (i == 0 && j < 3) || (j == 0 && i < 3) || (i == 1 && j == 1)
						q := "SELECT id FROM t WHERE id=1 FOR " + wanted + " " + policy
						if compatible {
							queryLockRows(t, p, b, peer, q, true, "id", 1)
						} else if policy == "NOWAIT" {
							queryLockNowait(t, p, b, peer, q, true, "t")
						} else {
							queryLockRows(t, p, b, peer, q, true, "id")
						}
					})
				})
			}
		}
	}
}

func TestReferenceQueryLockOptionsTableWaits(t *testing.T) {
	for _, policy := range []string{"NOWAIT", "SKIP LOCKED"} {
		for _, prepared := range []bool{false, true} {
			t.Run(fmt.Sprintf("%s/%v", policy, prepared), func(t *testing.T) {
				queryLockScenario(t, func(t *testing.T, p *referencePair, b referenceBackend, a, peer *sql.Conn) {
					ap, bp := 0, 0
					if b.pg {
						ap, bp = referenceRowPIDs(t, p.ctx, a, peer)
					}
					// Prepare before the relation blocker so this exercises Execute admission.
					q := "SELECT id FROM t ORDER BY id FOR UPDATE " + policy
					var stmt *sql.Stmt
					var err error
					if prepared {
						stmt, err = peer.PrepareContext(p.ctx, q)
						if err != nil {
							t.Fatal(err)
						}
						defer stmt.Close()
					}
					holder := queryLockBegin(t, p.ctx, a)
					referenceLockExec(t, p.ctx, holder, "LOCK TABLE t IN ACCESS EXCLUSIVE MODE", 0)
					ctx, cancel := context.WithTimeout(p.ctx, 5*time.Second)
					defer cancel()
					var x referenceExecutor = peer
					if prepared {
						x = referenceHeldStatement{referenceExecutor: peer, statement: stmt, query: q}
					}
					done := referenceRowAsync(t, ctx, x, q, false, true, b.recorder.rawBackend)
					if b.pg {
						referenceRowWait(t, ctx, bp, ap)
					}
					if err := holder.Rollback(); err != nil {
						t.Fatal(err)
					}
					out := referenceLockResult(t, done)
					queryLockMigratedRows(t, out.rows, out.err, []string{"id"}, []string{"INT4"}, [][]any{{int64(1)}, {int64(2)}, {int64(3)}, {int64(4)}})
				})
			})
		}
	}
}
