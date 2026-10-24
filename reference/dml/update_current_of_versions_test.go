package reference_test

import (
	"context"
	"database/sql"
	"stuntgres/reference/fixture"
	"testing"
	"time"
)

// Latest-predecessor expectations follow PostgreSQL 18.6 TidNext and the
// ordinary UPDATE contract; the shared definition runs against the pinned oracle.
func TestReferenceUpdateCurrentOfVersionsAndArbitration(t *testing.T) {
	for _, lane := range []string{"own_insert", "own_before", "own_after", "peer_commit", "peer_rollback", "peer_delete", "peer_reinsert", "rr_update", "rr_delete"} {
		for _, prepared := range []bool{false, true} {
			t.Run(lane+map[bool]string{false: "/direct", true: "/prepared"}[prepared], func(t *testing.T) {
				updateCurrentOfReference(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
					run := updateFromRun(t, p, b, c)
					run(fixture.Exec("CREATE TABLE t(id INT,a INT)", 0))
					run(fixture.Exec("INSERT INTO t VALUES(1,10),(2,30)", 2))
					peer, err := b.DB.Conn(p.Ctx)
					if err != nil {
						t.Fatal(err)
					}
					defer peer.Close()
					begin := "BEGIN"
					if lane == "rr_update" || lane == "rr_delete" {
						begin += " ISOLATION LEVEL REPEATABLE READ"
					}
					run(fixture.Exec(begin, 0))
					defer c.ExecContext(p.Ctx, "ROLLBACK")
					if lane == "own_insert" {
						run(fixture.Exec("INSERT INTO t VALUES(3,10)", 1))
					}
					id := int64(1)
					if lane == "own_insert" {
						id = 3
					}
					initial := int64(10)
					if lane == "own_before" {
						run(fixture.Exec("UPDATE t SET a=20 WHERE id=1", 1))
						initial = 20
					}
					run(fixture.Exec("DECLARE c CURSOR FOR SELECT id FROM t WHERE id=$1 AND a=$2", 0, id, initial))
					run(currentOfFetch("FETCH FORWARD 1 FROM c", id))
					if lane == "own_after" {
						run(fixture.Exec("UPDATE t SET a=20 WHERE id=1", 1))
						initial = 20
					}
					if lane == "peer_commit" || lane == "peer_rollback" || lane == "peer_delete" || lane == "peer_reinsert" || lane == "rr_update" || lane == "rr_delete" {
						tx, e := peer.BeginTx(p.Ctx, nil)
						if e != nil {
							t.Fatal(e)
						}
						defer tx.Rollback()
						q := "UPDATE t SET a=20 WHERE id=1"
						if lane == "peer_delete" || lane == "peer_reinsert" || lane == "rr_delete" {
							q = "DELETE FROM t WHERE id=1"
						}
						fixture.LockExec(t, p.Ctx, tx, q, 1)
						if lane == "peer_reinsert" {
							fixture.LockExec(t, p.Ctx, tx, "INSERT INTO t VALUES(1,10)", 1)
						}
						if lane == "peer_rollback" {
							err = tx.Rollback()
						} else {
							err = tx.Commit()
						}
						if err != nil {
							t.Fatal(err)
						}
						if lane == "peer_commit" {
							initial = 20
						}
					}
					q := "UPDATE t SET a=a+1 WHERE CURRENT OF c RETURNING id,old.a AS before_a,new.a AS after_a"
					if lane == "rr_update" || lane == "rr_delete" {
						message := "could not serialize access due to concurrent update"
						if lane == "rr_delete" {
							message = "could not serialize access due to concurrent delete"
						}
						fixture.DeleteReferenceExecuteStep(t, p, c, b.Recorder, currentOfError(q, "40001", message, prepared))
						fixture.DeleteReferenceExecuteStep(t, p, c, b.Recorder, currentOfError(q, "25P02", "current transaction is aborted, commands ignored until end of transaction block", false))
					} else if lane == "peer_delete" || lane == "peer_reinsert" {
						s := fixture.Exec("UPDATE t SET a=a+1 WHERE CURRENT OF c", 0)
						s.Prepared = prepared
						run(s)
					} else {
						for n := int64(0); n < 2; n++ {
							s := fixture.Select(q, []string{"id", "before_a", "after_a"}, []string{"INT4", "INT4", "INT4"}, [][]any{{id, initial + n, initial + n + 1}})
							s.Prepared = prepared
							run(s)
						}
						run(fixture.Select("SELECT a FROM t WHERE id=$1", []string{"a"}, []string{"INT4"}, [][]any{{initial + 2}}, id))
					}
					run(fixture.Exec("ROLLBACK", 0))
					expected := [][]any{{int64(1), int64(10)}, {int64(2), int64(30)}}
					if lane == "peer_commit" || lane == "rr_update" {
						expected[0][1] = int64(20)
					}
					if lane == "peer_delete" || lane == "rr_delete" {
						expected = expected[1:]
					}
					run(fixture.Select("SELECT id,a FROM t ORDER BY id", []string{"id", "a"}, []string{"INT4", "INT4"}, expected))
				})
			})
		}
	}
	for _, mark := range []string{"UPDATE", "NO KEY UPDATE", "SHARE", "KEY SHARE"} {
		t.Run("lock/"+mark, func(t *testing.T) {
			updateCurrentOfReference(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
				run := updateFromRun(t, p, b, c)
				currentOfSeed(run)
				peer, err := b.DB.Conn(p.Ctx)
				if err != nil {
					t.Fatal(err)
				}
				t.Cleanup(func() {
					if err := peer.Close(); err != nil {
						t.Error(err)
					}
				})
				currentOfPeerRollback(t, c)
				run(fixture.Exec("BEGIN", 0))
				// Cleanup is registered before LockOperation so its cancel/join runs first.
				run(fixture.Exec("DECLARE c CURSOR FOR SELECT id FROM t WHERE id=1 FOR "+mark, 0))
				run(currentOfFetch("FETCH FORWARD 1 FROM c", 1))
				run(fixture.Exec("UPDATE t SET v='owner' WHERE CURRENT OF c", 1))
				fixture.LockExec(t, p.Ctx, peer, "UPDATE t SET v='other' WHERE id=2", 1)
				fixture.RunStep(t, p.Ctx, peer, b.Recorder, fixture.Select("SELECT v FROM t WHERE id=1", []string{"v"}, []string{"TEXT"}, [][]any{{"one"}}))
				ctx, cancel := context.WithTimeout(p.Ctx, 5*time.Second)
				defer cancel()
				pp, cp := 0, 0
				if b.PG {
					pp = fixture.LockPID(t, ctx, peer)
					cp = fixture.LockPID(t, ctx, c)
				}
				completed := make(chan struct{})
				await := fixture.LockOperation(t, ctx, func(ctx context.Context) fixture.RelationOutcome {
					defer close(completed)
					r, e := peer.ExecContext(ctx, "UPDATE t SET v=v||'-peer' WHERE id=1")
					return fixture.RelationOutcome{Result: r, Err: e}
				})
				if b.PG {
					currentOfWaitRow(t, ctx, pp, cp)
				} else {
					fixture.NativeUpdateRowWait(t, ctx, completed)
				}
				run(fixture.Exec("COMMIT", 0))
				o := await()
				if o.Err != nil {
					t.Fatal(o.Err)
				}
				fixture.LockAffected(t, o.Result, 1)
				run(fixture.Select("SELECT id,v FROM t ORDER BY id", []string{"id", "v"}, []string{"INT4", "TEXT"}, [][]any{{int64(1), "owner-peer"}, {int64(2), "other"}, {int64(3), "three"}}))
			})
		})
	}
	for _, ordering := range []string{"blocked", "commit_before_capture"} {
		for _, ownerRoute := range []string{"direct", "fresh", "held"} {
			for _, peerRoute := range []string{"direct", "fresh", "held"} {
				t.Run("peer_recheck/"+ordering+"/owner_"+ownerRoute+"/peer_"+peerRoute, func(t *testing.T) {
					currentOfPeerRecheck(t, ordering, ownerRoute, peerRoute)
				})
			}
		}
	}
}
