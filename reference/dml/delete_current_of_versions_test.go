package reference_test

import (
	"context"
	"database/sql"
	"stuntgres/reference/fixture"
	"testing"
	"time"
)

// PostgreSQL 18.0 follows current row identity through own replacements and
// rejects newer committed predecessors under repeatable read.
func TestReferenceDeleteCurrentOfVersionsAndArbitration(t *testing.T) {
	for _, phase := range []string{"own_insert", "own_update_before", "own_update_after", "rolled_back_insert"} {
		t.Run(phase, func(t *testing.T) {
			currentOfReference(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
				run := updateFromRun(t, p, b, c)
				currentOfSeed(run)
				run(fixture.Exec("BEGIN", 0))
				defer c.ExecContext(p.Ctx, "ROLLBACK")
				id := int64(1)
				want := "one"
				if phase == "own_insert" || phase == "rolled_back_insert" {
					id = 7
					want = "private"
					run(fixture.Exec("SAVEPOINT insertion", 0))
					run(fixture.Exec("INSERT INTO t VALUES(7,'private')", 1))
				}
				if phase == "own_update_before" {
					run(fixture.Exec("UPDATE t SET v='fresh' WHERE id=1", 1))
					want = "fresh"
				}
				run(fixture.Exec("DECLARE c CURSOR FOR SELECT id FROM t WHERE id=$1", 0, id))
				run(currentOfFetch("FETCH FORWARD 1 FROM c", id))
				if phase == "own_update_after" {
					run(fixture.Exec("UPDATE t SET v='fresh' WHERE id=1", 1))
					want = "fresh"
				}
				if phase == "rolled_back_insert" {
					// Cursor declared after the savepoint is closed; its old physical identity
					// must not survive in a reused name or target equal replacement values.
					run(fixture.Exec("ROLLBACK TO insertion", 0))
					run(fixture.Exec("INSERT INTO t VALUES(7,'private')", 1))
					currentOfFailure(t, p, b, c, currentOfError("DELETE FROM t WHERE CURRENT OF c", "34000", `cursor "c" does not exist`, false))
					run(fixture.Exec("DECLARE c CURSOR FOR SELECT id FROM t WHERE id=7", 0))
					currentOfFailure(t, p, b, c, currentOfError("DELETE FROM t WHERE CURRENT OF c", "24000", `cursor "c" is not positioned on a row`, false))
					run(currentOfFetch("FETCH FORWARD 1 FROM c", 7))
				}
				run(fixture.Select("DELETE FROM t WHERE CURRENT OF c RETURNING id,v", []string{"id", "v"}, []string{"INT4", "TEXT"}, [][]any{{id, want}}))
				run(fixture.Exec("ROLLBACK", 0))
				fixture.DeleteReferenceRows(t, p, c, b.Recorder, 1, 2, 3)
			})
		})
	}
	for _, lane := range []string{"peer_update_rc", "peer_delete_commit", "peer_delete_rollback", "peer_update_rr"} {
		for _, prepared := range []bool{false, true} {
			t.Run(lane+map[bool]string{false: "/direct", true: "/prepared"}[prepared], func(t *testing.T) {
				currentOfReference(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
					run := updateFromRun(t, p, b, c)
					currentOfSeed(run)
					peer, err := b.DB.Conn(p.Ctx)
					if err != nil {
						t.Fatal(err)
					}
					defer peer.Close()
					begin := "BEGIN"
					if lane == "peer_update_rr" {
						begin = "BEGIN ISOLATION LEVEL REPEATABLE READ"
					}
					run(fixture.Exec(begin, 0))
					defer c.ExecContext(p.Ctx, "ROLLBACK")
					run(fixture.Exec("DECLARE c CURSOR FOR SELECT id FROM t WHERE id=1", 0))
					run(currentOfFetch("FETCH FORWARD 1 FROM c", 1))
					h, err := peer.BeginTx(p.Ctx, nil)
					if err != nil {
						t.Fatal(err)
					}
					defer h.Rollback()
					hold := "UPDATE t SET v='fresh' WHERE id=1"
					if lane == "peer_delete_commit" || lane == "peer_delete_rollback" {
						hold = "DELETE FROM t WHERE id=1"
					}
					fixture.LockExec(t, p.Ctx, h, hold, 1)
					if lane == "peer_delete_rollback" {
						err = h.Rollback()
					} else {
						err = h.Commit()
					}
					if err != nil {
						t.Fatal(err)
					}
					q := "DELETE FROM t WHERE CURRENT OF c RETURNING id,v"
					s := fixture.Select(q, []string{"id", "v"}, []string{"INT4", "TEXT"}, [][]any{{int64(1), "fresh"}})
					s.Prepared = prepared
					if lane == "peer_delete_commit" {
						s.Rows = nil
					}
					if lane == "peer_delete_rollback" {
						s.Rows = [][]any{{int64(1), "one"}}
					}
					if lane == "peer_update_rr" {
						currentOfFailure(t, p, b, c, currentOfError(q, "40001", "could not serialize access due to concurrent update", prepared))
					} else {
						run(s)
					}
					run(fixture.Exec("ROLLBACK", 0))
					if lane == "peer_delete_commit" {
						fixture.DeleteReferenceRows(t, p, c, b.Recorder, 2, 3)
					} else {
						fixture.DeleteReferenceRows(t, p, c, b.Recorder, 1, 2, 3)
					}
				})
			})
		}
	}
	t.Run("locking_current_and_release", func(t *testing.T) {
		currentOfReference(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
			run := updateFromRun(t, p, b, c)
			currentOfSeed(run)
			peer, err := b.DB.Conn(p.Ctx)
			if err != nil {
				t.Fatal(err)
			}
			defer peer.Close()
			run(fixture.Exec("BEGIN", 0))
			defer c.ExecContext(p.Ctx, "ROLLBACK")
			run(fixture.Exec("DECLARE c CURSOR FOR SELECT id FROM t WHERE id=1 FOR UPDATE", 0))
			run(currentOfFetch("FETCH FORWARD 1 FROM c", 1))
			fixture.LockExec(t, p.Ctx, peer, "UPDATE t SET v='other' WHERE id=2", 1)
			fixture.DeleteReferenceRows(t, p, peer, b.Recorder, 1, 2, 3)
			ctx, cancel := context.WithTimeout(p.Ctx, 5*time.Second)
			defer cancel()
			var pp, cp int
			if b.PG {
				pp = fixture.LockPID(t, ctx, peer)
				cp = fixture.LockPID(t, ctx, c)
			}
			started := make(chan struct{})
			await := fixture.LockOperation(t, ctx, func(ctx context.Context) fixture.RelationOutcome {
				close(started)
				r, e := peer.ExecContext(ctx, "UPDATE t SET v='released' WHERE id=1")
				return fixture.RelationOutcome{Result: r, Err: e}
			})
			<-started
			if b.PG {
				currentOfWaitRow(t, ctx, pp, cp)
			}
			// The native proof is the exact outcome after deleting the locked current
			// identity and releasing its owner; internal units supply the pending seam.
			run(fixture.Exec("DELETE FROM t WHERE CURRENT OF c", 1))
			run(fixture.Exec("COMMIT", 0))
			o := await()
			if o.Err != nil {
				t.Fatal(o.Err)
			}
			fixture.LockAffected(t, o.Result, 0)
			fixture.DeleteReferenceRows(t, p, c, b.Recorder, 2, 3)
		})
	})
}

func currentOfWaitRow(t *testing.T, ctx context.Context, waiter, holder int) {
	t.Helper()
	for {
		var waiting bool
		err := fixture.Admin.QueryRowContext(ctx, `SELECT EXISTS(SELECT 1 FROM pg_locks WHERE pid=$1 AND locktype='transactionid' AND NOT granted) AND $2=ANY(pg_blocking_pids($1))`, waiter, holder).Scan(&waiting)
		if err != nil {
			t.Fatal(err)
		}
		if waiting {
			return
		}
		if ctx.Err() != nil {
			t.Fatal(ctx.Err())
		}
	}
}
