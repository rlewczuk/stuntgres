package reference_test

import (
	"context"
	"stuntgres/reference/fixture"
	"testing"
	"time"
)

func TestReferenceViewLocksFeatureSmoke(t *testing.T) {
	fixture.RoleReferenceCleanup(t, []string{"view_locks_smoke_reader"})
	viewLocksScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend) {
		a, aid := blockingConn(t, p, b)
		w, wid := blockingConn(t, p, b)
		o, _ := blockingConn(t, p, b)
		reader, _ := blockingConn(t, p, b)
		namespaceExec(t, p, b, a, "CREATE TABLE view_locks_smoke(v INT)", "CREATE ROLE view_locks_smoke_reader")
		oid := viewLocksOID(t, p, o, "SELECT 'view_locks_smoke'::regclass::oid")
		db := viewLocksDatabase(t, p, o)
		if b.PG {
			namespaceExec(t, p, b, a, "GRANT USAGE ON SCHEMA public TO view_locks_smoke_reader")
		} else {
			// Preserve table ownership/ACL denial while admitting namespace lookup.
			namespaceExec(t, p, b, a, "ALTER SCHEMA public OWNER TO view_locks_smoke_reader")
		}
		namespaceExec(t, p, b, reader, "SET ROLE view_locks_smoke_reader")
		fixture.RunStep(t, p.Ctx, reader, b.Recorder, fixture.ErrorDiagnostic("SELECT * FROM view_locks_smoke", fixture.ControlsReferenceDiagnostic("42501", "permission denied for table view_locks_smoke")))
		q := "LOCK TABLE view_locks_smoke IN ACCESS EXCLUSIVE MODE"
		namespaceExec(t, p, b, w, "BEGIN")
		stmt, err := w.PrepareContext(p.Ctx, q)
		if err != nil {
			t.Fatal(err)
		}
		t.Cleanup(func() { _ = stmt.Close() })
		namespaceExec(t, p, b, a, "BEGIN", q)
		holder := viewLocksOwner(t, p, b, o, aid)
		before := time.Now()
		ctx, cancel := context.WithCancel(p.Ctx)
		defer cancel()
		start := func(ctx context.Context) func() fixture.RelationOutcome {
			return fixture.LockOperation(t, ctx, func(ctx context.Context) fixture.RelationOutcome {
				r, err := stmt.ExecContext(ctx)
				return fixture.RelationOutcome{Result: r, Err: err}
			})
		}
		await := start(ctx)
		rows := viewLocksPoll(t, p, b, reader, "relation=$1", func(r []viewLockRow) bool { return len(r) == 2 && viewLocksHasPending(r, wid, "AccessExclusiveLock") }, oid)
		pending := viewLocksPending(t, rows, wid, "AccessExclusiveLock", before)
		for _, r := range rows {
			viewLocksTarget(t, r, "relation", db, oid)
			if r.Mode != "AccessExclusiveLock" || !r.PID.Valid {
				t.Fatal("smoke relation mode/owner", r)
			}
			if r.PID.Int64 == int64(aid) {
				if !r.Granted || r.Owner != holder.Owner || r.Wait.Valid {
					t.Fatal("smoke holder", r, holder)
				}
			} else if r.PID.Int64 != int64(wid) || r.Granted {
				t.Fatal("smoke waiter", r)
			}
		}
		if pending.Owner == holder.Owner {
			t.Fatal("distinct transactions share owner", pending, holder)
		}
		viewLocksCancel(t, p, b, wid, cancel, await)
		viewLocksEmpty(t, p, b, reader, "relation=$1 AND pid=$2 AND NOT granted", oid, wid)
		// The holder is still real: cancellation was not simulated by releasing A.
		still := viewLocksOne(t, viewLocksRead(t, p.Ctx, b, reader, "relation=$1", oid))
		if !still.Granted || still.PID.Int64 != int64(aid) {
			t.Fatal("holder lost during cancel", still)
		}
		namespaceExec(t, p, b, w, "ROLLBACK", "BEGIN")
		before = time.Now()
		await = start(p.Ctx)
		rows = viewLocksPoll(t, p, b, reader, "relation=$1", func(r []viewLockRow) bool { return len(r) == 2 && viewLocksHasPending(r, wid, "AccessExclusiveLock") }, oid)
		viewLocksPending(t, rows, wid, "AccessExclusiveLock", before)
		namespaceExec(t, p, b, a, "COMMIT")
		blockingLockDone(t, await)
		granted := viewLocksOne(t, viewLocksRead(t, p.Ctx, b, reader, "relation=$1", oid))
		viewLocksTarget(t, granted, "relation", db, oid)
		if granted.PID.Int64 != int64(wid) || !granted.Granted || granted.Wait.Valid || granted.Mode != "AccessExclusiveLock" {
			t.Fatal("smoke granted", granted)
		}
		namespaceExec(t, p, b, w, "COMMIT")
		viewLocksEmpty(t, p, b, reader, "relation=$1", oid)
		namespaceExec(t, p, b, reader, "RESET ROLE")
		// Exact negative advisory identity/idle lifetime and actual row-XID wait
		// are part of this smoke, not baseline-only helper checks.
		viewLocksAdvisory(t, p, b)
		viewLocksRowKey(t, p, b, "row")
	})
}
