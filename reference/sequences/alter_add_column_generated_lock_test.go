package reference_test

import (
	"context"
	"database/sql/driver"
	"errors"
	"runtime"
	"stuntgres/reference/fixture"
	"testing"
)

func addGeneratedLockCancellation(t *testing.T) {
	addGeneratedRoutes(t, func(g *addGeneratedCase) {
		g.exec("CREATE TABLE g(a integer)", 0)
		holder, err := g.b.DB.Conn(g.p.Ctx)
		if err != nil {
			g.t.Fatal(err)
		}
		defer holder.Close()
		observer, err := g.b.DB.Conn(g.p.Ctx)
		if err != nil {
			g.t.Fatal(err)
		}
		defer observer.Close()
		q := "ALTER TABLE g ADD b integer GENERATED ALWAYS AS(a+1) STORED"
		executor := fixture.Executor(g.c)
		var held fixture.HeldStatement
		cancellationInvalidated := false
		closeHeld := func() {
			if held.Statement == nil {
				return
			}
			err := g.releaseHeld(held)
			held.Statement = nil
			// pq can invalidate this specific retained handle after lock cancellation.
			// No other statement or non-cancellation Close error is exempt.
			if err != nil && !(cancellationInvalidated && g.b.PG && errors.Is(err, driver.ErrBadConn)) {
				g.t.Error(err)
			}
		}
		defer closeHeld()
		if g.prepared {
			held = g.hold(q)
			executor = held
		}
		var workerPID, holderPID int
		var oid int64
		if g.b.PG {
			workerPID = fixture.LockPID(g.t, g.p.Ctx, g.c)
			holderPID = fixture.LockPID(g.t, g.p.Ctx, holder)
			oid = fixture.LockOID(g.t, g.p.Ctx, "g")
		}
		tx, err := holder.BeginTx(g.p.Ctx, nil)
		if err != nil {
			g.t.Fatal(err)
		}
		defer tx.Rollback()
		fixture.RunStep(g.t, g.p.Ctx, tx, g.b.Recorder, fixture.Exec("INSERT INTO g VALUES(1)", 1))
		ctx, cancel := context.WithCancel(g.p.Ctx)
		defer cancel()
		done := make(chan error, 1)
		go func() { _, err := executor.ExecContext(ctx, q); done <- err }()
		joined := false
		defer func() {
			cancel()
			if !joined {
				select {
				case <-done:
				case <-g.p.Ctx.Done():
					g.t.Error("generated ADD worker did not join", g.p.Ctx.Err())
				}
			}
		}()
		if g.b.PG {
			fixture.LockWaitBehind(g.t, g.p.Ctx, workerPID, holderPID, oid, "AccessExclusiveLock")
		} else {
			for {
				select {
				case err := <-done:
					joined = true
					g.t.Fatal("ADD completed while heap locked", err)
				case <-g.p.Ctx.Done():
					g.t.Fatal(g.p.Ctx.Err())
				default:
				}
				probe, err := observer.BeginTx(g.p.Ctx, nil)
				if err != nil {
					g.t.Fatal(err)
				}
				_, err = probe.ExecContext(g.p.Ctx, "LOCK TABLE g IN ACCESS SHARE MODE NOWAIT")
				if rollback := probe.Rollback(); rollback != nil {
					g.t.Fatal(rollback)
				}
				if err != nil {
					d := addGeneratedDiagnostic("55P03", `could not obtain lock on relation "g"`)
					fixture.AssertDiagnostic(g.t, err, &d)
					break
				}
				runtime.Gosched()
			}
		}
		if g.b.PG {
			var canceled bool
			if err := fixture.Admin.QueryRowContext(g.p.Ctx, "SELECT pg_cancel_backend($1)", workerPID).Scan(&canceled); err != nil || !canceled {
				g.t.Fatal(canceled, err)
			}
			cancellationInvalidated = true
		} else {
			cancel()
		}
		select {
		case err := <-done:
			joined = true
			if g.b.PG {
				d := addGeneratedDiagnostic("57014", "canceling statement due to user request")
				fixture.AssertDiagnostic(g.t, err, &d)
			} else if !errors.Is(err, context.Canceled) {
				g.t.Fatal(err)
			}
		case <-g.p.Ctx.Done():
			g.t.Fatal("generated ADD did not stop", g.p.Ctx.Err())
		}
		closeHeld()
		cancellationInvalidated = false
		if err := tx.Commit(); err != nil {
			g.t.Fatal(err)
		}
		g.unchanged([][]any{{int64(1)}})
		if g.prepared {
			held = g.hold(q)
			executor = held
		}
		fixture.RunStep(g.t, g.p.Ctx, executor, g.b.Recorder, fixture.Exec(q, 0))
		g.run(fixture.Select("SELECT * FROM g", []string{"a", "b"}, []string{"INT4", "INT4"}, [][]any{{int64(1), int64(2)}}))
	})
}
