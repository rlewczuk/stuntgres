package reference_test

import (
	"context"
	"database/sql"
	"testing"
	"time"

	"stuntgres/reference/fixture"
)

func currentOfPeerRollback(t *testing.T, conn *sql.Conn) {
	t.Helper()
	t.Cleanup(func() {
		ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
		defer cancel()
		// A canceled pq operation may have disposed of its physical connection.
		if _, err := conn.ExecContext(ctx, "ROLLBACK"); err != nil && err != sql.ErrConnDone {
			t.Error(err)
		}
	})
}

func currentOfPeerRecheck(t *testing.T, ordering, ownerRoute, peerRoute string) {
	updateCurrentOfReference(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, owner *sql.Conn) {
		run := updateFromRun(t, p, b, owner)
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
		currentOfPeerRollback(t, owner)
		currentOfPeerRollback(t, peer)
		const ownerSQL = "UPDATE t SET v='owner' WHERE CURRENT OF c"
		const peerSQL = "UPDATE t SET v=v||'-peer' WHERE id=1"
		prepare := func(conn *sql.Conn, query string) *sql.Stmt {
			stmt, err := conn.PrepareContext(p.Ctx, query)
			if err != nil {
				t.Fatal(err)
			}
			t.Cleanup(func() {
				if err := stmt.Close(); err != nil {
					t.Error(err)
				}
			})
			return stmt
		}
		var ownerHeld, peerHeld *sql.Stmt
		if ownerRoute == "held" {
			ownerHeld = prepare(owner, ownerSQL)
		}
		if peerRoute == "held" {
			peerHeld = prepare(peer, peerSQL)
		}
		declare := func() {
			run(fixture.Exec("BEGIN", 0))
			run(fixture.Exec("DECLARE c CURSOR FOR SELECT id FROM t WHERE id=1 FOR NO KEY UPDATE", 0))
			run(currentOfFetch("FETCH FORWARD 1 FROM c", 1))
		}
		if ownerHeld != nil {
			declare()
			fixture.RunStep(t, p.Ctx, fixture.HeldStatement{Executor: owner, Statement: ownerHeld, SQL: ownerSQL}, b.Recorder, fixture.Exec(ownerSQL, 1))
			run(fixture.Exec("ROLLBACK", 0))
		}
		if peerHeld != nil {
			peerRun := updateFromRun(t, p, b, peer)
			peerRun(fixture.Exec("BEGIN", 0))
			fixture.RunStep(t, p.Ctx, fixture.HeldStatement{Executor: peer, Statement: peerHeld, SQL: peerSQL}, b.Recorder, fixture.Exec(peerSQL, 1))
			peerRun(fixture.Exec("ROLLBACK", 0))
		}
		run(fixture.Select("SELECT id,v FROM t ORDER BY id", []string{"id", "v"}, []string{"INT4", "TEXT"}, [][]any{{int64(1), "one"}, {int64(2), "two"}, {int64(3), "three"}}))
		declare()
		ownerStep := fixture.Exec(ownerSQL, 1)
		if ownerRoute == "fresh" {
			ownerStep = fixture.Prepared(ownerStep)
		}
		if ownerHeld != nil {
			fixture.RunStep(t, p.Ctx, fixture.HeldStatement{Executor: owner, Statement: ownerHeld, SQL: ownerSQL}, b.Recorder, ownerStep)
		} else {
			run(ownerStep)
		}
		run(fixture.Select("SELECT v FROM t WHERE id=1", []string{"v"}, []string{"TEXT"}, [][]any{{"owner"}}))
		peerRun := updateFromRun(t, p, b, peer)
		peerRun(fixture.Exec("UPDATE t SET v='other' WHERE id=2", 1))
		peerRun(fixture.Select("SELECT v FROM t WHERE id=1", []string{"v"}, []string{"TEXT"}, [][]any{{"one"}}))
		// Fresh preparation occurs at the real invocation, not during held warmup.
		peerStmt := peerHeld
		if peerRoute == "fresh" {
			peerStmt = prepare(peer, peerSQL)
		}
		call := func(ctx context.Context) fixture.RelationOutcome {
			var result sql.Result
			var err error
			if peerStmt != nil {
				result, err = peerStmt.ExecContext(ctx)
			} else {
				result, err = peer.ExecContext(ctx, peerSQL)
			}
			return fixture.RelationOutcome{Result: result, Err: err}
		}
		var outcome fixture.RelationOutcome
		if ordering == "blocked" {
			ctx, cancel := context.WithTimeout(p.Ctx, 5*time.Second)
			defer cancel()
			pp, cp := 0, 0
			if b.PG {
				pp, cp = fixture.LockPID(t, ctx, peer), fixture.LockPID(t, ctx, owner)
			}
			completed := make(chan struct{})
			await := fixture.LockOperation(t, ctx, func(ctx context.Context) fixture.RelationOutcome { defer close(completed); return call(ctx) })
			if b.PG {
				currentOfWaitRow(t, ctx, pp, cp)
			} else {
				fixture.NativeUpdateRowWait(t, ctx, completed)
			}
			run(fixture.Exec("COMMIT", 0))
			outcome = await()
		} else {
			// This public ordering is separate from the private frontier/root gap proof.
			run(fixture.Exec("COMMIT", 0))
			outcome = call(p.Ctx)
		}
		if outcome.Err != nil {
			t.Fatal(outcome.Err)
		}
		fixture.LockAffected(t, outcome.Result, 1)
		peerRun(fixture.Select("SELECT id,v FROM t ORDER BY id", []string{"id", "v"}, []string{"INT4", "TEXT"}, [][]any{{int64(1), "owner-peer"}, {int64(2), "other"}, {int64(3), "three"}}))
	})
}
